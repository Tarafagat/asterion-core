package lab

import "os/exec"

// QEMUBackend crearía VMs Ubuntu desechables reales con aceleración KVM
// (imagen cloud + cloud-init para el aprovisionamiento, disco copy-on-write
// para que destruir la VM sea instantáneo y no toque la imagen base) —
// aislamiento de kernel completo, a costo de ser más lento de levantar que
// un contenedor. Ningún método más allá de Available() está implementado
// todavía — ver el comentario de paquete en lab.go.
type QEMUBackend struct{}

func (QEMUBackend) Name() string { return "qemu" }

func (QEMUBackend) Available() (bool, string) {
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		return false, "el binario 'qemu-system-x86_64' no está instalado (apt install qemu-system-x86 qemu-utils cloud-image-utils)"
	}
	if err := exec.Command("test", "-r", "/dev/kvm").Run(); err != nil {
		return false, "/dev/kvm no es accesible — sin aceleración KVM las VMs serían inviablemente lentas para pruebas repetibles"
	}
	return true, ""
}

func (QEMUBackend) Create(spec Spec) (Environment, error)   { return Environment{}, ErrNotImplemented }
func (QEMUBackend) List() ([]Environment, error)            { return nil, ErrNotImplemented }
func (QEMUBackend) Exec(id, command string) (string, error) { return "", ErrNotImplemented }
func (QEMUBackend) Destroy(id string) error                 { return ErrNotImplemented }
