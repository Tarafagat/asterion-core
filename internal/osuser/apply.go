package osuser

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
)

// Apply ejecuta el diff calculado por Plan. Quien llama (CLI o el
// servicio remoto por SSH) es responsable de pasar antes por
// safety.RequireSafeApply(safety.OSUserAdapter{}) — este paquete no
// importa internal/safety a propósito, para no crear un ciclo (safety ya
// depende de los paquetes que envuelve, nunca al revés).
func Apply(diff *Diff) (*Result, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("faltan privilegios: crear/modificar usuarios del sistema necesita root (sudo)")
	}
	if diff.AlreadyManaged {
		return nil, fmt.Errorf(
			"%q ya está administrado por Asterion (existe %s) — usar 'asterion local user remove %s' antes de volver a crearlo con otro nivel",
			diff.Username, diff.SudoFilePath, diff.Username,
		)
	}

	res := &Result{Diff: *diff}

	if !diff.UserExisted {
		if out, err := exec.Command("useradd", "-m", "-s", "/bin/bash", diff.Username).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("useradd falló: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	u, err := user.Lookup(diff.Username)
	if err != nil {
		return nil, fmt.Errorf("%q no aparece después de crearlo: %w", diff.Username, err)
	}
	diff.HomeDir = u.HomeDir
	res.Diff.HomeDir = u.HomeDir

	for _, g := range diff.GroupsToAdd {
		if out, err := exec.Command("usermod", "-aG", g, diff.Username).CombinedOutput(); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("no se pudo agregar al grupo %q: %v (%s)", g, err, strings.TrimSpace(string(out))))
		}
	}

	if diff.SSHKeyLine != "" {
		if err := installAuthorizedKey(u, diff.SSHKeyLine); err != nil {
			return nil, fmt.Errorf("no se pudo instalar la clave SSH: %w", err)
		}
	}

	if diff.SudoRule != "" {
		if err := writeSudoersFile(diff.SudoFilePath, diff.Username, diff.SudoRule); err != nil {
			return nil, fmt.Errorf("no se pudo configurar sudo: %w", err)
		}
	}

	res.Success = true
	return res, nil
}

// installAuthorizedKey crea ~/.ssh (0700) si hace falta y agrega la línea
// pedida a authorized_keys (0600) — ambos con el dueño correcto, nunca
// root, para que sshd los acepte.
func installAuthorizedKey(u *user.User, keyLine string) error {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}

	sshDir := u.HomeDir + "/.ssh"
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	if err := os.Chown(sshDir, uid, gid); err != nil {
		return err
	}

	akPath := sshDir + "/authorized_keys"
	f, err := os.OpenFile(akPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(strings.TrimRight(keyLine, "\n") + "\n"); err != nil {
		return err
	}
	return os.Chown(akPath, uid, gid)
}

// writeSudoersFile nunca escribe directo el archivo final: arma un
// temporal en el propio /etc/sudoers.d (mismo filesystem, para que el
// rename final sea atómico) y solo lo instala si `visudo -c` lo valida —
// una regla de sudo rota nunca llega a tocar el sistema real.
func writeSudoersFile(path, username, rule string) error {
	content := fmt.Sprintf("%s %s\n", username, rule)

	tmp, err := os.CreateTemp("/etc/sudoers.d", ".asterion-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op si ya se hizo rename

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o440); err != nil {
		return err
	}

	if out, err := exec.Command("visudo", "-c", "-f", tmpPath).CombinedOutput(); err != nil {
		return fmt.Errorf("la regla de sudo generada no pasó 'visudo -c' — no se instaló nada (%s)", strings.TrimSpace(string(out)))
	}

	return os.Rename(tmpPath, path)
}
