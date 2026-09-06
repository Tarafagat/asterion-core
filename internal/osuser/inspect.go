package osuser

import (
	"bufio"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// isSupportedDistro es deliberadamente conservador: solo Debian/Ubuntu
// (detectado por /etc/debian_version, presente en ambas) tienen mapeo de
// grupos/sudo verificado. Cualquier otra distro devuelve un error
// explícito desde Plan en vez de adivinar nombres de grupo que podrían no
// existir o significar otra cosa (ej. "wheel" en vez de "sudo").
func isSupportedDistro() bool {
	_, err := os.Stat("/etc/debian_version")
	return err == nil
}

// SupportStatus es el único chequeo EN VIVO de si esta máquina puntual
// puede de verdad crear/administrar usuarios de sistema — a diferencia de
// OSUserAdapter.Capabilities() (en internal/safety), que declara qué sabe
// hacer el código sin importar la distro o los privilegios del proceso
// actual. Lo consume tanto 'asterion local status' (para que el dashboard
// local sepa si mostrar el formulario o el motivo por el que no puede)
// como, en el futuro, cualquier otro llamador que necesite la misma
// respuesta honesta antes de intentar algo que va a fallar seguro.
type Support struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

func SupportStatus() Support {
	if !isSupportedDistro() {
		return Support{Supported: false, Reason: "distribución no soportada todavía (solo Debian/Ubuntu)"}
	}
	if os.Geteuid() != 0 {
		return Support{Supported: false, Reason: "faltan privilegios: hace falta correr como root (sudo)"}
	}
	return Support{Supported: true}
}

// userExists usa os/user.Lookup (NSS real, no un parseo propio de
// /etc/passwd) para preguntar si el usuario ya existe.
func userExists(username string) (bool, *user.User, error) {
	u, err := user.Lookup(username)
	if err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, u, nil
}

// currentGroups es la fuente real de "a qué grupos pertenece ya" — nunca
// se asume a partir de lo que Asterion recuerda haber pedido antes.
func currentGroups(username string) ([]string, error) {
	out, err := exec.Command("id", "-nG", username).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// groupExists confirma con getent, nunca asume que un grupo (ej. "docker")
// existe solo porque el nivel lo pide.
func groupExists(name string) bool {
	return exec.Command("getent", "group", name).Run() == nil
}

func containsStr(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}

func sudoFilePath(username string) string {
	return "/etc/sudoers.d/asterion-" + username
}

func sudoFileExists(username string) bool {
	_, err := os.Stat(sudoFilePath(username))
	return err == nil
}

// authorizedKeysContains compara por tipo+material (los dos primeros
// campos de la línea), ignorando el comentario final — para no instalar
// dos veces la misma clave solo porque el comentario cambió.
func authorizedKeysContains(homeDir, keyLine string) (bool, error) {
	path := homeDir + "/.ssh/authorized_keys"
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	wantType, wantMaterial, ok := splitKeyLine(keyLine)
	if !ok {
		return false, nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		gotType, gotMaterial, ok := splitKeyLine(scanner.Text())
		if ok && gotType == wantType && gotMaterial == wantMaterial {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func splitKeyLine(line string) (keyType, material string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}
