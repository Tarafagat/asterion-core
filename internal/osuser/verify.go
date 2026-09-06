package osuser

import (
	"fmt"
	"os"
	"strings"
)

// Verify reinspecciona el estado real y confirma que coincide con lo
// pedido — nunca confía en que Apply funcionó solo porque no devolvió
// error.
func Verify(spec Spec) error {
	exists, u, err := userExists(spec.Username)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%q no existe", spec.Username)
	}

	policy, err := Policy(spec.Level)
	if err != nil {
		return err
	}

	have, err := currentGroups(spec.Username)
	if err != nil {
		return fmt.Errorf("no se pudieron leer los grupos de %q: %w", spec.Username, err)
	}
	for _, g := range policy.Groups {
		if groupExists(g) && !containsStr(have, g) {
			return fmt.Errorf("%q debería estar en el grupo %q y no lo está", spec.Username, g)
		}
	}

	if policy.SudoRule != "" {
		data, err := os.ReadFile(sudoFilePath(spec.Username))
		if err != nil {
			return fmt.Errorf("no se pudo confirmar la regla de sudo de %q: %w", spec.Username, err)
		}
		if !strings.Contains(string(data), policy.SudoRule) {
			return fmt.Errorf("la regla de sudo de %q no coincide con el nivel %q", spec.Username, spec.Level)
		}
	}

	if spec.PublicKey != "" {
		present, err := authorizedKeysContains(u.HomeDir, spec.PublicKey)
		if err != nil {
			return fmt.Errorf("no se pudo confirmar la clave SSH de %q: %w", spec.Username, err)
		}
		if !present {
			return fmt.Errorf("la clave SSH no está instalada para %q", spec.Username)
		}
	}

	return nil
}
