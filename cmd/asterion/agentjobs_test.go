package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"asterion-core/internal/osuser"
)

// TestExecuteJob_CreateAndRemove_Live prueba el pipeline completo de un
// job de Agent (create -> Plan/Apply reales -> POST de resultado a un
// servidor de prueba -> remove -> Rollback real) contra el sistema
// operativo de verdad. Necesita root — se salta en cualquier máquina de
// desarrollo normal; se corre de verdad dentro de un contenedor Debian
// descartable como parte de la verificación de esta feature, nunca contra
// una máquina real.
func TestExecuteJob_CreateAndRemove_Live(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("necesita root — correr dentro de un contenedor descartable")
	}

	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	username := "agentjobtest"
	spec := osuser.Spec{Username: username, Level: osuser.LevelAdmin}
	payload, _ := json.Marshal(spec)

	executeJob(server.URL, "fake-key", PendingJob{ID: 1, JobType: "os_user_manage", Action: "create", Payload: payload})

	if len(received) != 1 {
		t.Fatalf("esperaba 1 resultado posteado, hay %d", len(received))
	}
	if received[0]["success"] != true {
		t.Fatalf("esperaba éxito en el create, recibí: %+v", received[0])
	}

	out, err := exec.Command("id", username).CombinedOutput()
	if err != nil {
		t.Fatalf("el usuario no existe de verdad después del job: %v (%s)", err, out)
	}
	t.Logf("id %s -> %s", username, out)

	sudoOut, _ := exec.Command("cat", "/etc/sudoers.d/asterion-"+username).CombinedOutput()
	t.Logf("sudoers: %s", sudoOut)
	if err := exec.Command("visudo", "-c", "-f", "/etc/sudoers.d/asterion-"+username).Run(); err != nil {
		t.Fatalf("el sudoers generado no es válido")
	}

	resultMap, ok := received[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("el resultado no trajo un 'result' con forma de mapa: %+v", received[0])
	}
	diffJSON, err := json.Marshal(resultMap["diff"])
	if err != nil {
		t.Fatalf("no se pudo re-serializar el diff: %v", err)
	}

	executeJob(server.URL, "fake-key", PendingJob{ID: 2, JobType: "os_user_manage", Action: "remove", Payload: diffJSON})

	if len(received) != 2 {
		t.Fatalf("esperaba 2 resultados posteados, hay %d", len(received))
	}
	if received[1]["success"] != true {
		t.Fatalf("esperaba éxito en el remove, recibí: %+v", received[1])
	}

	if out, err := exec.Command("id", username).CombinedOutput(); err == nil {
		t.Fatalf("el usuario debería haber sido borrado, sigue existiendo: %s", out)
	}
	if _, err := os.Stat("/etc/sudoers.d/asterion-" + username); err == nil {
		t.Fatalf("el drop-in de sudoers debería haber sido borrado")
	}
}
