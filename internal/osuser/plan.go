package osuser

import (
	"fmt"
	"path/filepath"
)

// Plan inspecciona el estado real de la máquina y arma exactamente lo que
// Apply haría — nunca muta nada. Capabilities: detect + inspect + plan.
func Plan(spec Spec) (*Diff, error) {
	if spec.Username == "" {
		return nil, fmt.Errorf("falta el nombre de usuario")
	}
	if !ValidLevel(spec.Level) {
		return nil, fmt.Errorf("nivel %q inválido (admin, operador, solo_lectura)", spec.Level)
	}
	if !isSupportedDistro() {
		return nil, fmt.Errorf("distribución no soportada todavía (solo Debian/Ubuntu) — no se intenta un mapeo de grupos adivinado")
	}

	policy, err := Policy(spec.Level)
	if err != nil {
		return nil, err
	}

	diff := &Diff{
		Username:     spec.Username,
		Level:        spec.Level,
		SudoRule:     policy.SudoRule,
		SudoFilePath: sudoFilePath(spec.Username),
	}

	wanted := dedupe(append(append([]string{}, policy.Groups...), spec.ExtraGroups...))

	exists, u, err := userExists(spec.Username)
	if err != nil {
		return nil, fmt.Errorf("no se pudo verificar si %q ya existe: %w", spec.Username, err)
	}
	diff.UserExisted = exists

	if exists {
		diff.HomeDir = u.HomeDir
		diff.AlreadyManaged = sudoFileExists(spec.Username)

		have, err := currentGroups(spec.Username)
		if err != nil {
			return nil, fmt.Errorf("no se pudieron leer los grupos actuales de %q: %w", spec.Username, err)
		}
		for _, g := range wanted {
			if groupExists(g) && !containsStr(have, g) {
				diff.GroupsToAdd = append(diff.GroupsToAdd, g)
			}
		}

		if spec.PublicKey != "" {
			present, err := authorizedKeysContains(u.HomeDir, spec.PublicKey)
			if err != nil {
				return nil, fmt.Errorf("no se pudo leer authorized_keys de %q: %w", spec.Username, err)
			}
			diff.SSHKeyAlreadyPresent = present
			if !present {
				diff.SSHKeyLine = spec.PublicKey
			}
		}
	} else {
		diff.HomeDir = filepath.Join("/home", spec.Username)
		for _, g := range wanted {
			if groupExists(g) {
				diff.GroupsToAdd = append(diff.GroupsToAdd, g)
			}
		}
		diff.SSHKeyLine = spec.PublicKey
	}

	return diff, nil
}
