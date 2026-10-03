package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Tarafagat/asterion-language/agcaspec"

	agcabot "github.com/Tarafagat/asterion-graph-cognitive-architecture/bot"
	agcacompiler "github.com/Tarafagat/asterion-graph-cognitive-architecture/compiler"
	agcaruntime "github.com/Tarafagat/asterion-graph-cognitive-architecture/runtime"
)

// graphCmd integra Asterion Graph Cognitive Architecture (AGCA) dentro de
// este CLI — repo hermano github.com/Tarafagat/asterion-graph-cognitive-architecture
// (clonado al lado de este, mismo criterio que asterion-lab/
// asterion-language/asterion-plugin-contract), que a su vez compila el
// .asterion vía asterion-language/agcaspec (namespace AGCA.* — DSL
// separado de Provider.*/Lab.*/Contract.*/System.*, ver
// asterion-language/spec/grammar.md).
//
// Estado real, sin adornos: 'validate'/'inspect'/'run' ya construyen una
// Intelligence de verdad (Cognitive Graph + Neuron Registry + Agent
// Scheduler + Capability Registry) y corren un ciclo cognitivo completo
// — pero con UNA sola neurona de verdad invocable de punta a punta
// (una DeterministicNeuron de referencia, por keywords, que el propio
// runtime siempre trae). Neuronas GGUF/remotas declaradas en el .asterion
// se registran y se listan, pero no tienen backend real todavía
// (ErrNotImplemented si algo las invoca) — ver
// asterion-graph-cognitive-architecture/runtime/runtime.go. Tampoco hay
// todavía un Executive Agent que infiera capabilities desde lenguaje
// natural (ver runtime.RunCognitiveCycle): 'run'/'bot run' piden la
// capability de neurona explícita, nunca la adivinan.
func graphCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "graph",
		Short: "Asterion Graph Cognitive Architecture (AGCA): declara y corre una inteligencia cognitiva desde un .asterion",
	}
	root.AddCommand(graphValidateCmd(), graphInspectCmd(), graphRunCmd(), graphBotCmd())
	root.AddCommand(graphCognitiveCmds()...)
	return root
}

func intelligenceFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "intelligence", "", "Nombre de variable de la AGCA.intelligence(...) a usar — obligatorio si el archivo declara más de una")
}

func graphValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <archivo.asterion>",
		Short: "Parsea y compila el archivo a una inteligencia AGCA — nunca construye ni corre nada",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := agcacompiler.CompileFile(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s — %d intelligence(s), %d graph(s), %d neuron(s), %d swarm(s), %d agent(s), %d capability requirement(s), %d memory(s), %d policy(s), %d bot(s), %d secret(s), %d tool(s), %d capability contract(s)\n",
				args[0], len(spec.Intelligences), len(spec.Graphs), len(spec.Neurons), len(spec.Swarms), len(spec.Agents),
				len(spec.Capabilities), len(spec.Memories), len(spec.Policies), len(spec.Bots), len(spec.Secrets),
				len(spec.Tools), len(spec.ToolCaps))
			return nil
		},
	}
}

func graphInspectCmd() *cobra.Command {
	var intelligence string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect <archivo.asterion>",
		Short: "Construye la inteligencia (grafo, neuronas, swarms, agentes, capabilities, bots) y muestra su estado — sin correr ningún ciclo cognitivo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := agcacompiler.CompileFile(args[0])
			if err != nil {
				return err
			}
			rt, err := agcaruntime.Build(spec, intelligence)
			if err != nil {
				return err
			}
			return printInspect(rt, asJSON)
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

type neuronStatus struct {
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Privacy   string   `json:"privacy"`
	Healthy   bool     `json:"healthy"`
	Abilities []string `json:"capabilities"`
}

type inspectReport struct {
	Intelligence      string         `json:"intelligence"`
	Graph             string         `json:"graph,omitempty"`
	Neurons           []neuronStatus `json:"neurons"`
	Swarms            []string       `json:"swarms"`
	Agents            []string       `json:"agents"`
	RequiredCaps      []string       `json:"required_capabilities"`
	Bots              []string       `json:"bots"`
	Imports           []string       `json:"imports"`
	Secrets           []string       `json:"secrets"`
	ToolCapabilities  []string       `json:"tool_capabilities"`
	AgentPermissions  []string       `json:"agent_permissions"`
	DiscoveredCaps    []string       `json:"discovered_capabilities"`
	DiscoveredSecrets []string       `json:"discovered_secrets"`
	GraphNodeCount    int            `json:"graph_node_count"`
	ExperienceCount   int            `json:"experience_count"`
}

func buildInspectReport(rt *agcaruntime.Runtime) inspectReport {
	report := inspectReport{Intelligence: rt.Intelligence.Name, GraphNodeCount: rt.Graph.Len(), ExperienceCount: rt.Experience.Len()}
	if rt.GraphSpec != nil {
		report.Graph = rt.GraphSpec.Name
	}
	for _, n := range rt.Neurons.All() {
		m := n.Manifest()
		report.Neurons = append(report.Neurons, neuronStatus{Name: m.Name, Provider: m.Provider, Privacy: m.Privacy, Healthy: m.Healthy, Abilities: m.Capabilities})
	}
	for _, s := range rt.Swarms {
		report.Swarms = append(report.Swarms, fmt.Sprintf("%s (instances=%s)", s.Name, s.Instances))
	}
	for _, a := range rt.Agents {
		report.Agents = append(report.Agents, fmt.Sprintf("%s (strategy=%s)", a.Name, a.Strategy))
	}
	for _, c := range rt.RequiredCaps {
		report.RequiredCaps = append(report.RequiredCaps, c.Capability)
	}
	for _, b := range rt.Bots {
		report.Bots = append(report.Bots, fmt.Sprintf("%s (interface=%s)", b.Name, b.Interface))
	}
	for _, imp := range rt.Imports {
		report.Imports = append(report.Imports, fmt.Sprintf("%s <- %s (plugins: %v)", imp.VarName, imp.Path, imp.PluginNames))
	}
	for _, s := range rt.Secrets {
		if s.From != "" {
			report.Secrets = append(report.Secrets, fmt.Sprintf("%s (derivado de %s.%s, %s)", s.Name, s.From, s.FromPlugin, s.Field))
		} else {
			report.Secrets = append(report.Secrets, fmt.Sprintf("%s (source=%s)", s.Name, s.Source))
		}
	}
	for _, c := range rt.Tools.All() {
		state := "declarada (sin handler — no ejecutable)"
		if rt.Tools.Implemented(c.ID) {
			state = "ejecutable"
		}
		report.ToolCapabilities = append(report.ToolCapabilities,
			fmt.Sprintf("%s [%s] effects=%v requires=%v — %s", c.ID, c.Tool, c.Effects, c.Requires, state))
	}
	for _, a := range rt.Agents {
		report.AgentPermissions = append(report.AgentPermissions,
			fmt.Sprintf("%s allow=%v deny=%v", a.Name, a.Allow, a.Deny))
	}
	for _, dc := range rt.DiscoveredCapabilities {
		if dc.Resolved {
			report.DiscoveredCaps = append(report.DiscoveredCaps, fmt.Sprintf("%s.%s: %v", dc.ImportVar, dc.Plugin, dc.Capabilities))
		} else {
			report.DiscoveredCaps = append(report.DiscoveredCaps, fmt.Sprintf("%s.%s: no resuelto (%s)", dc.ImportVar, dc.Plugin, dc.Reason))
		}
	}
	for _, ds := range rt.DiscoveredSecrets {
		report.DiscoveredSecrets = append(report.DiscoveredSecrets, fmt.Sprintf("%s.%s: %s (%s)", ds.ImportVar, ds.Plugin, ds.Key, ds.Label))
	}
	return report
}

func printInspect(rt *agcaruntime.Runtime, asJSON bool) error {
	report := buildInspectReport(rt)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Printf("Intelligence: %s\n", report.Intelligence)
	if report.Graph != "" {
		fmt.Printf("Graph: %s (%d nodo(s))\n", report.Graph, report.GraphNodeCount)
	}
	fmt.Println("Neuronas:")
	for _, n := range report.Neurons {
		health := "fuera de servicio"
		if n.Healthy {
			health = "disponible"
		}
		fmt.Printf("  - %s [%s] privacy=%s capabilities=%v — %s\n", n.Name, n.Provider, n.Privacy, n.Abilities, health)
	}
	if len(report.Swarms) > 0 {
		fmt.Println("Swarms:")
		for _, s := range report.Swarms {
			fmt.Printf("  - %s\n", s)
		}
	}
	if len(report.Agents) > 0 {
		fmt.Println("Agentes:")
		for _, a := range report.Agents {
			fmt.Printf("  - %s\n", a)
		}
	}
	if len(report.RequiredCaps) > 0 {
		fmt.Println("Capabilities de plugin requeridas:")
		for _, c := range report.RequiredCaps {
			fmt.Printf("  - %s\n", c)
		}
	}
	if len(report.Bots) > 0 {
		fmt.Println("Bots:")
		for _, b := range report.Bots {
			fmt.Printf("  - %s\n", b)
		}
	}
	if len(report.Imports) > 0 {
		fmt.Println("Imports:")
		for _, imp := range report.Imports {
			fmt.Printf("  - %s\n", imp)
		}
	}
	if len(report.Secrets) > 0 {
		fmt.Println("Secrets:")
		for _, s := range report.Secrets {
			fmt.Printf("  - %s\n", s)
		}
	}
	if len(report.ToolCapabilities) > 0 {
		fmt.Println("Capabilities de Tool (lo único que AGCA puede ejecutar):")
		for _, c := range report.ToolCapabilities {
			fmt.Printf("  - %s\n", c)
		}
	}
	if len(report.AgentPermissions) > 0 {
		fmt.Println("Permisos por agente (deny-by-default):")
		for _, a := range report.AgentPermissions {
			fmt.Printf("  - %s\n", a)
		}
	}
	if len(report.DiscoveredCaps) > 0 {
		fmt.Println("Capabilities descubiertas (de plugins importados):")
		for _, dc := range report.DiscoveredCaps {
			fmt.Printf("  - %s\n", dc)
		}
	}
	if len(report.DiscoveredSecrets) > 0 {
		fmt.Println("Secretos descubiertos (config_schema del propio plugin, sin declarar nada acá):")
		for _, ds := range report.DiscoveredSecrets {
			fmt.Printf("  - %s\n", ds)
		}
	}
	return nil
}

func graphRunCmd() *cobra.Command {
	var intelligence, goal, capability string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "run <archivo.asterion>",
		Short: "Corre UN ciclo cognitivo (perceive -> neurona -> merge en el grafo -> experience) para un goal puntual",
		Long: "Construye la inteligencia y corre exactamente un ciclo cognitivo (ver el pseudocódigo\n" +
			"del Apéndice C de 'Camino a la AGI'): crea un nodo de observación con el goal, busca\n" +
			"neuronas saludables que declaren --capability, invoca la primera que responda, agrega\n" +
			"el resultado como nodo de insight (con procedencia) y registra la experiencia. No hay\n" +
			"un Executive Agent infiriendo la capability desde el goal en lenguaje natural todavía\n" +
			"— hay que declararla explícita.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if goal == "" {
				return fmt.Errorf("--goal es obligatorio")
			}
			spec, err := agcacompiler.CompileFile(args[0])
			if err != nil {
				return err
			}
			rt, err := agcaruntime.Build(spec, intelligence)
			if err != nil {
				return err
			}
			result, err := rt.RunCognitiveCycle(context.Background(), goal, capability)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}
			fmt.Printf("✓ neurona usada: %s\n", result.NeuronUsed)
			fmt.Printf("  output: %s\n", result.Output)
			fmt.Printf("  grafo: observación %s -> insight %s (trace %s)\n", result.ObservationNode, result.InsightNode, result.TraceID)
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().StringVar(&goal, "goal", "", "El objetivo/pregunta a procesar (obligatorio)")
	cmd.Flags().StringVar(&capability, "capability", "classification", "Capacidad de NEURONA requerida para este ciclo (no confundir con capability de plugin)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el resultado como JSON en vez de texto")
	return cmd
}

func graphBotCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "bot",
		Short: "Corre un bot (interfaz hacia una inteligencia AGCA ya declarada) — nunca una inteligencia aparte",
	}
	root.AddCommand(graphBotRunCmd())
	return root
}

func graphBotRunCmd() *cobra.Command {
	var intelligence, botName string
	cmd := &cobra.Command{
		Use:   "run <archivo.asterion>",
		Short: "Abre una sesión de terminal contra un AGCA.bot(...) declarado — una línea por goal",
		Long: "Construye la inteligencia del bot y abre un loop de terminal: cada línea que\n" +
			"escribes se trata como un goal, corre un ciclo cognitivo completo (ver 'graph run')\n" +
			"y se imprime el resultado — Ctrl+D o 'exit' para salir. El bot en sí no piensa: es\n" +
			"la interfaz (ver § 16 de 'Camino a la AGI', 'un bot no debe ser una inteligencia\n" +
			"separada por defecto') hacia la misma Intelligence que 'graph run' usa.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := agcacompiler.CompileFile(args[0])
			if err != nil {
				return err
			}
			rt, err := agcaruntime.Build(spec, intelligence)
			if err != nil {
				return err
			}
			decl, err := findBotDecl(rt, botName)
			if err != nil {
				return err
			}
			b, err := agcabot.New(rt, decl)
			if err != nil {
				return err
			}
			return runBotTerminal(b)
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().StringVar(&botName, "bot", "", "Nombre de variable del AGCA.bot(...) a correr — obligatorio si el archivo declara más de uno")
	return cmd
}

func findBotDecl(rt *agcaruntime.Runtime, name string) (agcaspec.BotDecl, error) {
	if name != "" {
		for _, b := range rt.Bots {
			if b.VarName == name {
				return b, nil
			}
		}
		return agcaspec.BotDecl{}, fmt.Errorf("graph bot run: no se declaró ningún AGCA.bot(...) de nombre %q para esta intelligence", name)
	}
	switch len(rt.Bots) {
	case 0:
		return agcaspec.BotDecl{}, fmt.Errorf("graph bot run: esta intelligence no declara ningún AGCA.bot(...)")
	case 1:
		return rt.Bots[0], nil
	default:
		names := make([]string, len(rt.Bots))
		for i, b := range rt.Bots {
			names[i] = b.VarName
		}
		return agcaspec.BotDecl{}, fmt.Errorf("graph bot run: hay %d bots (%v) — indica cuál con --bot", len(rt.Bots), names)
	}
}

func runBotTerminal(b *agcabot.Bot) error {
	fmt.Printf("Bot %q (interface=%s) — escribe un goal por línea, Ctrl+D o 'exit' para salir.\n", b.Decl.Name, b.Decl.Interface)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			return nil
		}
		goal := strings.TrimSpace(scanner.Text())
		if goal == "" {
			continue
		}
		if goal == "exit" {
			return nil
		}
		result, err := b.Ask(context.Background(), goal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Printf("%s\n", result.Output)
	}
}
