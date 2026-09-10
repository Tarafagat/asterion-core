package dbbackup

import (
	"context"
	"net"
	"testing"
)

// listenPort abre un listener TCP en un puerto efímero de localhost y
// devuelve el puerto real asignado — nunca pega contra 3306/5432 de
// verdad, así el test no depende de qué haya (o no) instalado en la
// máquina que corre `go test`.
func listenPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no pude abrir un listener de prueba: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return ln, port
}

func TestConfirmMySQL_GreetingReal(t *testing.T) {
	ln, port := listenPort(t)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Paquete de saludo mínimo: 3 bytes de longitud + 1 de sequence id +
		// protocol version 10 (0x0a) — lo único que confirmMySQL mira.
		_, _ = conn.Write([]byte{0x01, 0x00, 0x00, 0x00, 0x0a})
	}()

	if !confirmMySQL(context.Background(), port) {
		t.Fatal("esperaba confirmar MySQL con un greeting real, dio false")
	}
}

func TestConfirmMySQL_ProtocolDistinto(t *testing.T) {
	ln, port := listenPort(t)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// protocol version 9 — versión vieja que confirmMySQL no reconoce,
		// o simplemente algo que no es MySQL escuchando ahí.
		_, _ = conn.Write([]byte{0x01, 0x00, 0x00, 0x00, 0x09})
	}()

	if confirmMySQL(context.Background(), port) {
		t.Fatal("no debería confirmar MySQL con protocol version distinto de 10")
	}
}

func TestConfirmMySQL_NadaEscuchando(t *testing.T) {
	ln, port := listenPort(t)
	ln.Close() // nadie escucha ya en este puerto

	if confirmMySQL(context.Background(), port) {
		t.Fatal("no debería confirmar MySQL en un puerto cerrado")
	}
}

func TestConfirmPostgres_SSLRequestReal(t *testing.T) {
	for _, respByte := range []byte{'S', 'N'} {
		respByte := respByte
		t.Run(string(respByte), func(t *testing.T) {
			ln, port := listenPort(t)
			defer ln.Close()

			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				buf := make([]byte, 8)
				if _, err := conn.Read(buf); err != nil {
					return
				}
				_, _ = conn.Write([]byte{respByte})
			}()

			if !confirmPostgres(context.Background(), port) {
				t.Fatalf("esperaba confirmar Postgres con respuesta %q, dio false", respByte)
			}
		})
	}
}

func TestConfirmPostgres_RespuestaInesperada(t *testing.T) {
	ln, port := listenPort(t)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 8)
		if _, err := conn.Read(buf); err != nil {
			return
		}
		_, _ = conn.Write([]byte{'X'}) // ni 'S' ni 'N' — no es Postgres real
	}()

	if confirmPostgres(context.Background(), port) {
		t.Fatal("no debería confirmar Postgres con una respuesta que no es 'S' ni 'N'")
	}
}

func TestConfirmPostgres_NadaEscuchando(t *testing.T) {
	ln, port := listenPort(t)
	ln.Close()

	if confirmPostgres(context.Background(), port) {
		t.Fatal("no debería confirmar Postgres en un puerto cerrado")
	}
}

func TestDiscover_SinNadaEscuchando(t *testing.T) {
	// No hay forma de "reservar" el puerto 3306/5432 de mentira sin pisar
	// un servicio real que pudiera estar corriendo en la máquina de CI —
	// este test solo confirma que Discover nunca explota ni devuelve nil,
	// siempre una lista (posiblemente vacía).
	got := Discover(context.Background())
	if got == nil {
		t.Fatal("Discover no debería devolver nil nunca, incluso sin nada detectado")
	}
}
