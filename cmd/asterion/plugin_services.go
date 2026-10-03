package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tarafagat/asterion-plugin-contract/apc"

	"asterion-core/internal/plugins"
	"asterion-core/internal/pluginsvc"
)

// pluginServicesCmd administra los servicios EXTERNOS que un plugin
// declara en su plugin.yaml (una base de datos, un Redis — ver
// Contract.service(...) en Asterion Language y apc.ServiceSpec).
//
// El orden nunca cambia: primero DETECTAR lo que ya existe, después
// CONFIGURAR adentro de eso (crear la base y el usuario que falten), y
// recién con --create, si no hay nada, levantar el motor en un
// contenedor. Asterion no arranca servicios que nadie pidió.
func pluginServicesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "services <plugin>",
		Short: "Detecta, configura y conecta los servicios externos que un plugin necesita (bases de datos, Redis)",
		Long: "Sin subcomando, solo DETECTA y reporta: qué servicios declara el plugin, qué hay\n" +
			"corriendo, qué falta y qué ya quedó configurado. No toca nada.\n\n" +
			"  up       configura usando lo que YA existe: crea la base y el usuario que falten\n" +
			"           dentro de un motor que ya está corriendo, y vuelca host/puerto/usuario/\n" +
			"           contraseña a la config cifrada del plugin. Con --create, si no hay ningún\n" +
			"           motor, levanta uno en un contenedor (solo con ese flag).\n" +
			"  connect  modo manual: apuntar un servicio a uno que ya exista en otro lado (una base\n" +
			"           administrada, un Redis remoto) cargando los datos a mano, sin crear nada.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesStatus(args[0])
		},
	}
	root.AddCommand(pluginServicesUpCmd(), pluginServicesConnectCmd())
	return root
}

// loadServices trae el plugin, su config actual y lo que declara.
func loadServices(name string) (plugins.Installed, []apc.ServiceSpec, map[string]string, error) {
	installed, err := plugins.Get(name)
	if err != nil {
		return plugins.Installed{}, nil, nil, err
	}
	if len(installed.Manifest.Services) == 0 {
		return installed, nil, nil, fmt.Errorf(
			"%q no declara ningún servicio externo en su plugin.yaml — si necesita una base o un Redis, "+
				"decláralo con Contract.service(...) (ver asterion-language/spec/grammar.md)", installed.Name)
	}
	config, err := plugins.GetConfig(name)
	if err != nil {
		return plugins.Installed{}, nil, nil, err
	}
	return installed, installed.Manifest.Services, config, nil
}

func runServicesStatus(name string) error {
	installed, specs, config, err := loadServices(name)
	if err != nil {
		return err
	}

	fmt.Printf("Servicios externos de %q\n\n", installed.Name)
	var pending int
	for _, spec := range specs {
		st := pluginsvc.Detect(spec, installed.Name, config)
		marker := "✗"
		switch {
		case st.Ready():
			marker = "✓"
		case st.Reachable:
			marker = "•"
		}
		if !st.Ready() {
			pending++
		}
		fmt.Printf(" %s %-12s %-9s %s\n", marker, spec.Name, spec.Kind, st.Detail)
		if keys := mappedKeys(spec); len(keys) > 0 {
			fmt.Printf("      vuelca a: %s\n", strings.Join(keys, ", "))
		}
	}

	fmt.Println()
	if pending == 0 {
		fmt.Printf("Todo listo. Arráncalo con: asterion plugin start %s\n", installed.Name)
		return nil
	}
	if pending == 1 {
		fmt.Printf("Falta 1 servicio. Opciones:\n")
	} else {
		fmt.Printf("Faltan %d servicios. Opciones:\n", pending)
	}
	fmt.Printf("  asterion plugin services up %s              # usar lo que ya está corriendo\n", installed.Name)
	fmt.Printf("  asterion plugin services up %s --create     # además, levantar en contenedor lo que falte\n", installed.Name)
	fmt.Printf("  asterion plugin services connect %s <svc>   # apuntar a uno remoto, cargando los datos a mano\n", installed.Name)
	return nil
}

// mappedKeys son las claves del config_schema que este servicio
// completa — lo que hace concreto qué deja de tipearse a mano.
func mappedKeys(spec apc.ServiceSpec) []string {
	var out []string
	for _, k := range []string{spec.MapsHost, spec.MapsPort, spec.MapsUser, spec.MapsPassword, spec.MapsDatabase, spec.MapsURL} {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

func pluginServicesUpCmd() *cobra.Command {
	var create, rotate bool
	var adminUser, adminPassword, only string
	cmd := &cobra.Command{
		Use:   "up <plugin>",
		Short: "Configura los servicios que faltan usando lo que ya existe, y vuelca la conexión a la config del plugin",
		Long: "Para cada servicio declarado: si ya hay un motor corriendo, crea ahí dentro la base\n" +
			"y el usuario que falten (con una contraseña generada) y escribe host/puerto/usuario/\n" +
			"contraseña en la config cifrada del plugin. Un usuario que ya existía NO cambia de\n" +
			"contraseña: rotarla en silencio dejaría con la vieja a quien ya la estuviera usando.\n\n" +
			"Las credenciales de administrador del motor viajan por variable de entorno al proceso\n" +
			"hijo (PGPASSWORD/MYSQL_PWD), nunca como argumento — un password en la línea de\n" +
			"comandos queda en el historial y es visible en 'ps aux' para cualquier usuario de la\n" +
			"máquina.\n\n" +
			"--create es lo ÚNICO que autoriza a levantar un motor en un contenedor cuando no hay\n" +
			"ninguno. Sin ese flag, un servicio sin motor se reporta y se sigue con los demás.\n\n" +
			"Antes de guardar nada se entra con la credencial resuelta: si no sirve, no se guarda.\n" +
			"Por eso un usuario que ya existe en el motor y del que no hay contraseña guardada corta\n" +
			"acá en vez de dejar en la config algo que no funciona — ese caso se resuelve con\n" +
			"--rotate-password, o cargando la contraseña real con 'services connect'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesUp(args[0], only, create, rotate, pluginsvc.AdminCreds{User: adminUser, Password: adminPassword})
		},
	}
	cmd.Flags().BoolVar(&create, "create", false, "Si falta el motor, levantarlo en un contenedor (nunca se hace sin este flag)")
	cmd.Flags().StringVar(&adminUser, "admin-user", "", "Usuario administrador del motor ya existente (default: postgres / root)")
	cmd.Flags().StringVar(&adminPassword, "admin-password", "", "Contraseña de ese administrador — se usa en el momento y no se guarda")
	cmd.Flags().StringVar(&only, "service", "", "Configurar solo este servicio, por nombre")
	cmd.Flags().BoolVar(&rotate, "rotate-password", false, "Asignarle una contraseña nueva a un usuario que ya existe (si algo más lo usa, deja de andar)")
	return cmd
}

func runServicesUp(name, only string, create, rotate bool, admin pluginsvc.AdminCreds) error {
	installed, specs, config, err := loadServices(name)
	if err != nil {
		return err
	}
	ctx := context.Background()

	updates := map[string]string{}
	var failed []string

	for _, spec := range specs {
		if only != "" && spec.Name != only {
			continue
		}
		fmt.Printf("--- %s (%s) ---\n", spec.Name, spec.Kind)

		st := pluginsvc.Detect(spec, installed.Name, config)

		// Con un puerto guardado inválido no se sigue: el estándar puede
		// estar respondiendo, y se configuraría la base adentro de OTRO
		// servidor que no es el que el plugin va a usar.
		if st.BadPort != "" {
			fmt.Printf("  ✗ %s\n\n", st.Detail)
			failed = append(failed, spec.Name)
			continue
		}

		if st.Ready() {
			fmt.Printf("  = ya estaba configurado y responde en %s:%d — sin cambios\n\n", st.Host, st.Port)
			continue
		}

		// Nada corriendo: solo con --create se levanta algo.
		if !st.Reachable {
			if !create {
				fmt.Printf("  ✗ %s\n", st.Detail)
				fmt.Printf("    Para levantarlo acá: asterion plugin services up %s --service %s --create\n", installed.Name, spec.Name)
				fmt.Printf("    Para apuntarlo a uno remoto: asterion plugin services connect %s %s\n\n", installed.Name, spec.Name)
				failed = append(failed, spec.Name)
				continue
			}
			newSt, adminForNew, err := createEngine(ctx, spec, installed.Name, st)
			if err != nil {
				fmt.Printf("  ✗ %v\n\n", err)
				failed = append(failed, spec.Name)
				continue
			}
			st, admin = newSt, adminForNew
		}

		res, err := pluginsvc.Provision(ctx, spec, st, pluginsvc.ProvisionOptions{
			Admin:            admin,
			ExistingPassword: pluginsvc.StoredPassword(spec, config),
			RotatePassword:   rotate,
		})
		if err != nil {
			fmt.Printf("  ✗ %v\n\n", err)
			failed = append(failed, spec.Name)
			continue
		}
		for _, a := range res.Actions {
			fmt.Printf("  ✓ %s\n", a)
		}
		for k, v := range res.Values {
			updates[k] = v
			config[k] = v // para que el siguiente Detect vea lo ya resuelto
		}
		fmt.Printf("  → config: %s\n\n", strings.Join(sortedKeys(res.Values), ", "))
	}

	if len(updates) > 0 {
		if err := plugins.SetConfig(installed.Name, updates); err != nil {
			return err
		}
		fmt.Printf("✓ Config de %q actualizada: %s, cifradas en disco\n", installed.Name, plural(len(updates), "clave", "claves"))
		fmt.Printf("  Las contraseñas generadas no se imprimen acá. Quedan cifradas y el plugin las recibe\n")
		fmt.Printf("  al arrancar; 'asterion plugin config show %s' muestra el resto y enmascara los secretos.\n", installed.Name)
	}
	if len(failed) > 0 {
		fmt.Printf("\nQuedaron sin resolver: %s\n", strings.Join(failed, ", "))
		return fmt.Errorf("%s sin configurar", plural(len(failed), "servicio", "servicios"))
	}
	fmt.Printf("\nArrancalo con: asterion plugin start %s\n", installed.Name)
	return nil
}

// createEngine levanta el motor en un contenedor y espera a que acepte
// conexiones. Devuelve el estado actualizado y las credenciales de
// administrador del motor recién creado (que solo viven en memoria: se
// usan para crear la base del plugin y se descartan).
func createEngine(ctx context.Context, spec apc.ServiceSpec, pluginName string, st pluginsvc.Status) (pluginsvc.Status, pluginsvc.AdminCreds, error) {
	if reason := pluginsvc.DockerUnavailableReason(); reason != "" {
		return st, pluginsvc.AdminCreds{}, fmt.Errorf(
			"no puedo levantar un %s en un contenedor: %s.\n    Alternativa sin Docker: apunta este servicio a uno que ya exista con 'asterion plugin services connect %s %s'",
			spec.Kind, reason, pluginName, spec.Name)
	}

	adminPassword, err := pluginsvc.GenerateAdminPassword()
	if err != nil {
		return st, pluginsvc.AdminCreds{}, err
	}
	fmt.Printf("  … levantando %s en un contenedor (pediste --create)\n", spec.Kind)

	c, err := pluginsvc.CreateContainer(ctx, spec, pluginName, st.Port, adminPassword)
	if err != nil {
		return st, pluginsvc.AdminCreds{}, err
	}
	fmt.Printf("  ✓ contenedor %q (%s) escuchando en 127.0.0.1:%d\n", c.Name, c.Image, c.Port)

	// Dejar asentado que este motor es un contenedor nuestro: Provision lo
	// usa para hablarle con el cliente que ya viene adentro, sin exigir uno
	// instalado en el host.
	st.Host, st.Port, st.Container = "127.0.0.1", c.Port, c.Name
	// 90s: un postgres o un mysql recién creados inicializan su cluster
	// antes de aceptar la primera query, y en una máquina cargada (o con
	// la imagen recién bajada) eso tarda bastante más que unos segundos.
	fmt.Printf("  … esperando que el motor termine de inicializar\n")
	if err := pluginsvc.WaitReady(ctx, c, spec, adminPassword, 90*time.Second); err != nil {
		return st, pluginsvc.AdminCreds{}, err
	}
	st.Reachable = true
	fmt.Printf("  ✓ el %s ya responde\n", spec.Kind)

	admin := pluginsvc.AdminCreds{Password: adminPassword}
	if spec.Kind == pluginsvc.KindPostgres {
		admin.User = "postgres"
	} else {
		admin.User = "root"
	}
	return st, admin, nil
}

func pluginServicesConnectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect <plugin> <servicio>",
		Short: "Apunta un servicio a uno que YA existe en otro lado, cargando los datos a mano",
		Long: "El modo manual: para una base administrada, un Redis remoto, o cualquier servicio que\n" +
			"Asterion no pueda (ni deba) crear. Pide host, puerto, usuario, contraseña y base uno\n" +
			"por uno — solo los que el manifiesto declaró mapear — y los guarda en la config\n" +
			"cifrada del plugin, sin crear ni modificar nada del otro lado.\n\n" +
			"Al terminar prueba la conexión y dice si respondió, en vez de dar por buenos unos\n" +
			"datos que nunca se usaron.\n\n" +
			"Ojo, no confundir con 'asterion plugin connect', que es otra cosa: ese vincula el\n" +
			"plugin a un proyecto de Asterion Cloud. Este apunta un servicio externo del plugin\n" +
			"(su base, su Redis) a una instancia que ya existe.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesConnect(args[0], args[1])
		},
	}
	return cmd
}

func runServicesConnect(name, serviceName string) error {
	installed, specs, config, err := loadServices(name)
	if err != nil {
		return err
	}

	var spec apc.ServiceSpec
	var found bool
	for _, s := range specs {
		if s.Name == serviceName {
			spec, found = s, true
			break
		}
	}
	if !found {
		var names []string
		for _, s := range specs {
			names = append(names, s.Name)
		}
		return fmt.Errorf("%q no declara ningún servicio llamado %q — declarados: %s",
			installed.Name, serviceName, strings.Join(names, ", "))
	}

	if !stdinIsTerminal() {
		return fmt.Errorf("no hay una terminal interactiva — usa 'asterion plugin config set %s %s=... %s=...' directamente",
			installed.Name, spec.MapsHost, spec.MapsPort)
	}

	fmt.Printf("\nConectando %q (%s) de %q a un servicio que ya existe.\n", spec.Name, spec.Kind, installed.Name)
	fmt.Println("Enter deja el valor actual. Nada de esto crea ni modifica nada del otro lado.")

	updates := map[string]string{}

	// check valida la FORMA de lo cargado. Vale la pena por una razón
	// concreta: un puerto que no es un número lo ignora Detect en silencio y
	// vuelve al estándar, así que el plugin terminaría hablándole a otro
	// servidor mientras 'plugin services' informa que todo está bien. Un
	// error acá, con la persona mirando, es infinitamente más barato.
	ask := func(key, label string, secret bool, check func(string) error) {
		if key == "" {
			return
		}
		fmt.Printf("\n%s\n", label)
		if cur := config[key]; cur != "" && !secret {
			fmt.Printf("  (actual: %s)\n", cur)
		} else if cur != "" {
			fmt.Printf("  (ya hay uno guardado)\n")
		}

		for {
			fmt.Printf("  %s = ", key)
			var v string
			if secret {
				v = readSecretLine()
			} else {
				v = strings.TrimSpace(trimNewline(readLine()))
			}
			if v == "" {
				return // Enter: dejar lo que ya había
			}
			if check != nil {
				if err := check(v); err != nil {
					fmt.Printf("  ✗ %v\n", err)
					continue
				}
			}
			updates[key] = v
			config[key] = v
			return
		}
	}

	ask(spec.MapsHost, "Host", false, checkHost)
	ask(spec.MapsPort, fmt.Sprintf("Puerto (estándar de %s si lo dejás vacío)", spec.Kind), false, checkPort)
	ask(spec.MapsUser, "Usuario", false, nil)
	ask(spec.MapsPassword, "Contraseña", true, nil)
	ask(spec.MapsDatabase, "Nombre de la base", false, nil)
	ask(spec.MapsURL, "URL de conexión completa (si el plugin usa una sola variable)", true, checkURL)

	if len(updates) == 0 {
		fmt.Println("\nNo cargaste ningún valor — nada que guardar.")
		return nil
	}
	if err := plugins.SetConfig(installed.Name, updates); err != nil {
		return err
	}
	fmt.Printf("\n✓ Config de %q actualizada: %s\n", installed.Name, strings.Join(sortedKeys(updates), ", "))

	// Probar de verdad, en vez de dar por buenos datos que nunca se usaron.
	st := pluginsvc.Detect(spec, installed.Name, config)
	if st.Reachable {
		fmt.Printf("✓ %s:%d respondió\n", st.Host, st.Port)
	} else {
		fmt.Printf("⚠ %s:%d no respondió — los datos quedaron guardados igual, pero revisa host/puerto/firewall\n", st.Host, st.Port)
	}
	return nil
}

// checkHost: un host, no una URL. Pegar "postgresql://..." en el campo de
// host es el error más fácil de cometer acá, y el que peor se diagnostica
// después.
func checkHost(v string) error {
	if strings.Contains(v, "://") {
		return fmt.Errorf("eso es una URL completa, no un host — acá va solo el nombre o la IP (ej. db.interno, 10.0.0.5)")
	}
	if strings.ContainsAny(v, " \t/@") {
		return fmt.Errorf("un host no lleva espacios, '/' ni '@' — acá va solo el nombre o la IP")
	}
	return nil
}

func checkPort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("el puerto tiene que ser un número (ej. 5432), no %q", v)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("%d no es un puerto válido — tiene que estar entre 1 y 65535", n)
	}
	return nil
}

func checkURL(v string) error {
	u, err := url.Parse(v)
	if err != nil {
		return fmt.Errorf("no pude interpretar eso como una URL: %v", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("falta el esquema o el host — tiene que ser algo como postgresql://usuario:clave@host:5432/base")
	}
	return nil
}

// plural evita los "1 servicio(s)" — el CLI lo lee una persona.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// sortedKeys da un orden estable a lo que se imprime — dos corridas
// iguales tienen que leerse iguales, y el orden de un map en Go no lo es.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
