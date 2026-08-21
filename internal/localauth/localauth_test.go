package localauth

import (
	"os"
	"testing"
)

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
}

func TestEnsureToken_FirstRunGeneratesAndSecondRunReuses(t *testing.T) {
	withTempHome(t)

	token1, isNew1, err := EnsureToken()
	if err != nil {
		t.Fatalf("primera corrida: %v", err)
	}
	if !isNew1 || token1 == "" {
		t.Fatalf("esperaba un token nuevo en la primera corrida, isNew=%v token=%q", isNew1, token1)
	}

	token2, isNew2, err := EnsureToken()
	if err != nil {
		t.Fatalf("segunda corrida: %v", err)
	}
	if isNew2 {
		t.Fatal("la segunda corrida no debería generar un token nuevo si ya había uno")
	}
	if token2 != "" {
		t.Fatal("EnsureToken nunca debe devolver el token existente en texto plano — solo el hash queda guardado")
	}

	ok, err := Verify(token1)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("el token generado en la primera corrida debería seguir siendo válido")
	}
}

func TestVerify_RejectsWrongToken(t *testing.T) {
	withTempHome(t)
	if _, _, err := EnsureToken(); err != nil {
		t.Fatal(err)
	}
	ok, err := Verify("un-token-inventado-que-no-es-el-real")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("un token incorrecto nunca debe verificar como válido")
	}
}

func TestRotate_InvalidatesPreviousToken(t *testing.T) {
	withTempHome(t)
	original, _, err := EnsureToken()
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if rotated == original {
		t.Fatal("rotate debería generar un token distinto del anterior")
	}

	okOld, _ := Verify(original)
	if okOld {
		t.Fatal("el token anterior a un rotate no debería seguir siendo válido")
	}
	okNew, _ := Verify(rotated)
	if !okNew {
		t.Fatal("el token nuevo después de rotate debería ser válido")
	}
}

func TestLocalAuthFilePermissions(t *testing.T) {
	withTempHome(t)
	if _, _, err := EnsureToken(); err != nil {
		t.Fatal(err)
	}
	filePath, err := path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("local-auth.yaml debería tener permisos 0600 (solo el dueño), tiene %o", perm)
	}
}

func TestGetStatus_ReportsNotConfiguredWhenMissing(t *testing.T) {
	withTempHome(t)
	status, err := GetStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured {
		t.Fatal("sin ningún EnsureToken/Rotate previo, Configured debería ser false")
	}
}
