package runtime

import "testing"

// Estos tests cubren exactamente el escenario que motivó todo el Safety
// Lab (ver el prompt de referencia): "instalé ufw, se me cortó el SSH y
// no puedo volver a entrar". AssessSSHRisk es lo único que hoy existe
// contra ese escenario — no hay Apply, así que esto es lo que hay que
// tener bien probado.

func TestAssessSSHRisk_CriticalWhenActiveFirewallMissesRule(t *testing.T) {
	ssh := SSHInfo{ServiceActive: true, Ports: []string{"2222"}, RunningOverSSH: true}
	fw := FirewallInspection{
		Backend: "ufw", Readable: true,
		RawRules: "Status: active\n\nTo                         Action      From\n--                         ------      ----\n80/tcp                     ALLOW       Anywhere",
	}
	got := AssessSSHRisk(ssh, fw)
	if got.Level != RiskCritical {
		t.Fatalf("esperaba CRITICAL cuando el firewall está activo sin regla para el puerto SSH real, obtuve %q (%s)", got.Level, got.Reason)
	}
}

func TestAssessSSHRisk_LowWhenRuleProtectsPort(t *testing.T) {
	ssh := SSHInfo{ServiceActive: true, Ports: []string{"22"}}
	fw := FirewallInspection{
		Backend: "ufw", Readable: true,
		RawRules: "Status: active\n\n22/tcp                     ALLOW       Anywhere",
	}
	got := AssessSSHRisk(ssh, fw)
	if got.Level != RiskLow {
		t.Fatalf("esperaba LOW cuando hay una regla ALLOW para el puerto SSH, obtuve %q (%s)", got.Level, got.Reason)
	}
}

func TestAssessSSHRisk_UnknownWhenFirewallNotReadable(t *testing.T) {
	// El caso más común en la práctica (sin sudo): nunca debe reportarse
	// LOW solo porque no se pudo leer el firewall — sería el falso
	// negativo exacto que este chequeo existe para evitar.
	ssh := SSHInfo{ServiceActive: true, Ports: []string{"22"}}
	fw := FirewallInspection{Backend: "ufw", Readable: false, Detail: "necesita root"}
	got := AssessSSHRisk(ssh, fw)
	if got.Level != RiskUnknown {
		t.Fatalf("esperaba UNKNOWN cuando no se pudo leer el firewall, obtuve %q — nunca debe asumirse LOW sin evidencia", got.Level)
	}
}

func TestAssessSSHRisk_LowWhenSSHInactive(t *testing.T) {
	got := AssessSSHRisk(SSHInfo{ServiceActive: false}, FirewallInspection{})
	if got.Level != RiskLow {
		t.Fatalf("esperaba LOW cuando SSH ni siquiera está activo, obtuve %q", got.Level)
	}
}

func TestAssessSSHRisk_MediumWhenFirewallInactive(t *testing.T) {
	ssh := SSHInfo{ServiceActive: true, Ports: []string{"22"}}
	fw := FirewallInspection{Backend: "ufw", Readable: true, RawRules: "Status: inactive"}
	got := AssessSSHRisk(ssh, fw)
	if got.Level != RiskMedium {
		t.Fatalf("esperaba MEDIUM cuando ufw está instalado pero inactivo (sin riesgo hoy, riesgo si se habilita sin excepción), obtuve %q", got.Level)
	}
}

func TestPortAllowedInUFW(t *testing.T) {
	raw := "Status: active\n\nTo                         Action      From\n--                         ------      ----\n2222/tcp                   ALLOW       Anywhere\n80/tcp                     ALLOW       Anywhere"
	if !portAllowedInUFW(raw, []string{"2222"}) {
		t.Fatal("esperaba encontrar la regla ALLOW para 2222/tcp")
	}
	if portAllowedInUFW(raw, []string{"9999"}) {
		t.Fatal("no debería encontrar una regla para un puerto que no está en la lista")
	}
}
