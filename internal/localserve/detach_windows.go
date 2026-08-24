//go:build windows

package localserve

import "os/exec"

// En Windows no hay Setsid — mismo alcance no implementado que
// internal/plugins.setDetached para la misma plataforma.
func SetDetached(cmd *exec.Cmd) {}
