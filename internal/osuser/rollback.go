package osuser

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Rollback deshace EXACTAMENTE lo que Apply hizo, según el Diff guardado
// — nunca un "borrar todo lo que tenga que ver con este usuario" genérico.
// Si el usuario ya existía antes del Apply, solo se revierte lo que ese
// Apply agregó; un usuario preexistente jamás se borra por accidente.
func Rollback(diff *Diff) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("faltan privilegios: revertir usuarios del sistema necesita root (sudo)")
	}

	if !diff.UserExisted {
		// userdel -r borra el usuario y su home, pero NO toca
		// /etc/sudoers.d — sin esto, el drop-in de sudo quedaría huérfano
		// (detectado en vivo durante la verificación de este paquete).
		if diff.SudoRule != "" {
			_ = os.Remove(diff.SudoFilePath)
		}
		if out, err := exec.Command("userdel", "-r", diff.Username).CombinedOutput(); err != nil {
			return fmt.Errorf("userdel falló: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if diff.SudoRule != "" {
		if err := os.Remove(diff.SudoFilePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("no se pudo borrar %s: %w", diff.SudoFilePath, err)
		}
	}

	for _, g := range diff.GroupsToAdd {
		// best-effort: si el usuario ya no está en el grupo (alguien lo
		// sacó a mano mientras tanto), no es un error de este rollback.
		_ = exec.Command("gpasswd", "-d", diff.Username, g).Run()
	}

	if diff.SSHKeyLine != "" {
		if err := removeAuthorizedKeyLine(diff.HomeDir, diff.SSHKeyLine); err != nil {
			return fmt.Errorf("no se pudo quitar la clave SSH: %w", err)
		}
	}

	return nil
}

// removeAuthorizedKeyLine borra solo la línea exacta (por tipo+material,
// como authorizedKeysContains) que este Apply agregó — nunca reescribe el
// archivo entero ni toca otras claves que ya estuvieran ahí.
func removeAuthorizedKeyLine(homeDir, keyLine string) error {
	path := homeDir + "/.ssh/authorized_keys"
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	wantType, wantMaterial, ok := splitKeyLine(keyLine)
	if !ok {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	for _, line := range lines {
		gotType, gotMaterial, ok := splitKeyLine(line)
		if ok && gotType == wantType && gotMaterial == wantMaterial {
			continue // esta es la línea que se agregó — se omite
		}
		out = append(out, line)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600)
}
