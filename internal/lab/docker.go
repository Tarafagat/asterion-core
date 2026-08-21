package lab

import "os/exec"

// DockerBackend crearía entornos desechables como contenedores Ubuntu con
// systemd (imagen tipo jrei/systemd-ubuntu o equivalente propia) — mucho
// más rápido de crear/destruir que una VM completa, aunque con menos
// aislamiento de kernel que QEMU. Ningún método más allá de Available()
// está implementado todavía — ver el comentario de paquete en lab.go.
type DockerBackend struct{}

func (DockerBackend) Name() string { return "docker" }

func (DockerBackend) Available() (bool, string) {
	if _, err := exec.LookPath("docker"); err != nil {
		return false, "el binario 'docker' no está en PATH"
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return false, "el binario 'docker' está pero el daemon no responde (¿está corriendo? ¿el usuario está en el grupo docker?)"
	}
	return true, ""
}

func (DockerBackend) Create(spec Spec) (Environment, error)   { return Environment{}, ErrNotImplemented }
func (DockerBackend) List() ([]Environment, error)            { return nil, ErrNotImplemented }
func (DockerBackend) Exec(id, command string) (string, error) { return "", ErrNotImplemented }
func (DockerBackend) Destroy(id string) error                 { return ErrNotImplemented }
