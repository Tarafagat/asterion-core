// Package osuser es el motor de aprovisionamiento de usuarios de sistema
// operativo de Asterion: localizar la instancia ya la tiene quien llama
// (este paquete asume que ya está corriendo en la máquina destino, o que
// alguien más abrió la sesión remota) — lo que este paquete resuelve es
// todo lo que sigue, siempre igual sin importar el proveedor cloud de
// abajo: crear el usuario, instalar su clave SSH, agregarlo a grupos,
// configurar sudo, y devolver un resultado estructurado que se pueda
// registrar en Asterion.
//
// Es una única fuente de verdad para "qué comandos correr" — tanto
// `asterion local user` (CLI, en esta misma máquina) como el camino
// remoto por SSH de Asterion Cloud (backend/app/services/osuser_service.py)
// implementan la MISMA política de niveles/comandos, documentada acá,
// igual que el manifiesto de plugin tiene una especificación única con
// una implementación por lenguaje.
//
// v1 asume Debian/Ubuntu (ver isSupportedDistro) y 3 niveles fijos, a
// propósito — no hay editor de políticas custom todavía.
package osuser

import "fmt"

// Level es el nivel de acceso que Asterion le asigna a un usuario DEL
// SISTEMA OPERATIVO en una instancia puntual — un eje completamente
// distinto del rol de un miembro de un proyecto de Asterion Cloud (eso es
// "quién puede editar esta instancia desde el dashboard"; esto es "qué
// puede hacer, en la propia máquina, el usuario Unix que se creó ahí").
type Level string

const (
	LevelAdmin       Level = "admin"
	LevelOperador    Level = "operador"
	LevelSoloLectura Level = "solo_lectura"
)

// ValidLevel confirma que el nivel es uno de los 3 presets conocidos —
// nunca se acepta un nivel arbitrario sin política definida.
func ValidLevel(l Level) bool {
	switch l {
	case LevelAdmin, LevelOperador, LevelSoloLectura:
		return true
	default:
		return false
	}
}

// LevelPolicy es lo que un nivel implica de verdad en Debian/Ubuntu.
type LevelPolicy struct {
	Groups      []string
	SudoRule    string // vacío == sin entrada de sudo para este nivel
	Description string
}

var levelPolicies = map[Level]LevelPolicy{
	LevelAdmin: {
		Groups:      []string{"sudo"},
		SudoRule:    "ALL=(ALL) NOPASSWD:ALL",
		Description: "acceso total, sudo sin pedir contraseña",
	},
	LevelOperador: {
		Groups:      []string{"docker"},
		SudoRule:    "ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart *, /usr/bin/systemctl status *, /usr/bin/journalctl *",
		Description: "puede reiniciar/inspeccionar servicios y usar Docker, sin sudo total",
	},
	LevelSoloLectura: {
		Groups:      nil,
		SudoRule:    "",
		Description: "solo puede iniciar sesión, sin ningún privilegio de sudo",
	},
}

// Policy devuelve la política real de un nivel, o error si no es uno de
// los 3 presets conocidos.
func Policy(l Level) (LevelPolicy, error) {
	p, ok := levelPolicies[l]
	if !ok {
		return LevelPolicy{}, fmt.Errorf("nivel %q desconocido (válidos: admin, operador, solo_lectura)", l)
	}
	return p, nil
}

// Spec es lo que se pide crear/asegurar.
type Spec struct {
	Username    string
	Level       Level
	ExtraGroups []string // grupos adicionales a los que ya implica el nivel
	PublicKey   string   // línea completa "ssh-ed25519 AAAA... comentario" — vacío si no se instala ninguna
}

// Diff es el resultado de Plan: exactamente lo que Apply haría, y lo
// mismo que Rollback necesita después para deshacerlo con precisión — no
// un "borrar todo lo relacionado con este usuario" genérico.
type Diff struct {
	Username             string   `json:"username"`
	Level                Level    `json:"level"`
	UserExisted          bool     `json:"user_existed"` // ya existía ANTES de este Apply
	HomeDir              string   `json:"home_dir"`
	GroupsToAdd          []string `json:"groups_to_add"` // solo los que el usuario todavía no tenía
	SudoRule             string   `json:"sudo_rule"`     // vacío si el nivel es solo_lectura
	SudoFilePath         string   `json:"sudo_file_path"`
	SSHKeyLine           string   `json:"ssh_key_line,omitempty"`
	SSHKeyAlreadyPresent bool     `json:"ssh_key_already_present"`
	AlreadyManaged       bool     `json:"already_managed"` // ya hay un sudoers.d de Asterion para este user
}

// Result es lo que Apply devuelve — se persiste tal cual (JSON) en el
// store local del CLI y, del lado de Asterion Cloud, en
// instance_os_users.applied_diff.
type Result struct {
	Diff     Diff     `json:"diff"`
	Success  bool     `json:"success"`
	Warnings []string `json:"warnings,omitempty"`
}
