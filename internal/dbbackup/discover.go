// Package dbbackup es lo que ejecuta el agente para los jobs
// database_discover/database_backup que Asterion Cloud encola (ver
// cmd/asterion/agentjobs.go::executeJob, y del otro lado
// asterion-cloud/backend/app/routers/agent.py::init_agent_backup/
// complete_agent_backup). No es una adaptación de infraestructura en el
// sentido de internal/safety — un dump no muta nada del sistema ni tiene
// un "Rollback" real, así que deliberadamente no se registra ahí.
//
// Discover es credential-less y best-effort, mismo espíritu que
// internal/cloudmeta: nunca falla, en el peor caso devuelve una lista
// vacía — no puede saber usuario/contraseña de la base, así que solo
// confirma motor+puerto escuchando localmente.
package dbbackup

import (
	"context"
	"io"
	"net"
	"strconv"
	"time"

	"asterion-core/internal/runtime"
)

// Detected es un motor de base de datos que Discover encontró escuchando
// localmente. Nunca incluye base/usuario — sin credenciales no hay forma
// de saber eso.
type Detected struct {
	Engine string `json:"engine"`
	Port   int    `json:"port"`
}

var defaultPorts = map[string]int{
	"mysql":    3306,
	"postgres": 5432,
}

const probeTimeout = 2 * time.Second

// Discover prueba los puertos default de MySQL/MariaDB y PostgreSQL en
// localhost. runtime.PortListening confirma que algo escucha ahí;
// confirmMySQL/confirmPostgres confirman DE VERDAD que es ese motor (y no
// cualquier otra cosa que casualmente esté en ese puerto) con un
// handshake mínimo que no requiere ninguna credencial.
func Discover(ctx context.Context) []Detected {
	detected := []Detected{}
	if runtime.PortListening("127.0.0.1", defaultPorts["mysql"]) && confirmMySQL(ctx, defaultPorts["mysql"]) {
		detected = append(detected, Detected{Engine: "mysql", Port: defaultPorts["mysql"]})
	}
	if runtime.PortListening("127.0.0.1", defaultPorts["postgres"]) && confirmPostgres(ctx, defaultPorts["postgres"]) {
		detected = append(detected, Detected{Engine: "postgres", Port: defaultPorts["postgres"]})
	}
	return detected
}

// confirmMySQL se conecta y lee el paquete de saludo inicial que MySQL/
// MariaDB manda apenas alguien conecta, sin que el cliente tenga que decir
// nada primero — el protocol version byte (offset 4 del primer paquete)
// es 10 (0x0a) en cualquier versión moderna de ambos motores. Alcanza
// para confirmar sin ninguna credencial.
func confirmMySQL(ctx context.Context, port int) bool {
	dialCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(probeTimeout))

	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return false
	}
	// header[0:3] = longitud del paquete (24 bits, little endian),
	// header[3] = sequence id, header[4] = protocol version.
	return header[4] == 0x0a
}

// confirmPostgres manda un SSLRequest — el único paquete que Postgres
// acepta ANTES de cualquier autenticación — y confirma por la única
// respuesta posible: un byte 'S' (soporta SSL) o 'N' (no lo soporta).
// Cualquier otra cosa (o silencio) significa que no es Postgres.
func confirmPostgres(ctx context.Context, port int) bool {
	dialCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(probeTimeout))

	// SSLRequest: longitud (int32 big endian, 8) + código mágico 80877103.
	sslRequest := []byte{0x00, 0x00, 0x00, 0x08, 0x04, 0xd2, 0x16, 0x2f}
	if _, err := conn.Write(sslRequest); err != nil {
		return false
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return false
	}
	return resp[0] == 'S' || resp[0] == 'N'
}
