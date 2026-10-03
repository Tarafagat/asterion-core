package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	agcacognition "github.com/Tarafagat/asterion-graph-cognitive-architecture/cognition"
	agcacompiler "github.com/Tarafagat/asterion-graph-cognitive-architecture/compiler"
	agcaruntime "github.com/Tarafagat/asterion-graph-cognitive-architecture/runtime"
)

// graphCognitiveCmds son los comandos de la capa de Experience (Segundo
// Principio de AGI de AGCA): actuar sobre un World eligiendo una
// capability declarada, y después poder responder qué se ejecutó, por
// qué, qué se esperaba, qué pasó de verdad y cómo cambió la certeza.
//
// 'act' NO ejecuta nada por su cuenta salvo que una capability tenga un
// handler atado en Go (ver runtime.BindHandler): un .asterion declara
// contratos, y declarar no es implementar. Sin handlers, 'act' sirve
// igual para ver la Decision completa — candidatos, scores, descartes y
// motivo — que es justamente lo que hace auditable al sistema.
func graphCognitiveCmds() []*cobra.Command {
	return []*cobra.Command{
		graphActCmd(),
		graphRolesCmd(),
		graphDecisionsCmd(),
		graphExplainCmd(),
		graphExperienceCmd(),
	}
}

// openRuntime compila el archivo, construye la Intelligence y le conecta
// la memoria persistente (decisiones, experiencias y certeza aprendida)
// — sin esto cada corrida del CLI sería amnésica.
func openRuntime(path, intelligence string, persist bool) (*agcaruntime.Runtime, error) {
	spec, err := agcacompiler.CompileFile(path)
	if err != nil {
		return nil, err
	}
	rt, err := agcaruntime.Build(spec, intelligence)
	if err != nil {
		return nil, err
	}
	if !persist {
		return rt, nil
	}
	dir, err := agcaruntime.DefaultPersistenceDir(rt.Intelligence.Name)
	if err != nil {
		return nil, err
	}
	if err := rt.WithPersistence(dir); err != nil {
		return nil, err
	}
	return rt, nil
}

func graphActCmd() *cobra.Command {
	var intelligence, goal, intent, agent, role, user string
	var asJSON, ephemeral bool
	cmd := &cobra.Command{
		Use:   "act <archivo.asterion>",
		Short: "Toma una decisión cognitiva sobre qué capability usar para un goal, la ejecuta si corresponde, y aprende del resultado",
		Long: "Corre el pipeline completo de la capa de Experience, en este orden obligatorio:\n\n" +
			"  Decision -> Policy Check -> Agent Permission Check -> Tool Contract\n" +
			"  -> Requirements -> Handler -> Result -> Evaluation -> Experience\n" +
			"  -> Confidence Update\n\n" +
			"La Decision se guarda SIEMPRE, se haya ejecutado o no: rechazar una acción por\n" +
			"falta de certeza (o por una política) es un resultado cognitivo legítimo, no un\n" +
			"error que se esconde. AGCA nunca inventa una capability que no exista ni ejecuta\n" +
			"código arbitrario — solo puede elegir entre los contratos declarados con\n" +
			"Tool.capability(...) que además tengan un handler atado.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if goal == "" {
				return fmt.Errorf("--goal es obligatorio")
			}
			if intent == "" {
				intent = goal
			}
			rt, err := openRuntime(args[0], intelligence, !ephemeral)
			if err != nil {
				return err
			}
			out, actErr := rt.Act(context.Background(), agcaruntime.ActRequest{
				Goal: goal, Intent: intent, Agent: agent, Role: role, User: user,
			})
			if out == nil {
				return actErr
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
				return actErr
			}
			printDecision(out.Decision)
			if actErr != nil {
				fmt.Printf("\nNo se ejecutó: %v\n", actErr)
				return nil // la decisión ya se explicó; no es una falla del comando
			}
			fmt.Printf("\nEjecutado: %s\n", out.Contract.ID)
			if len(out.Result.Output) > 0 {
				fmt.Printf("  salida: %v\n", out.Result.Output)
			}
			e := out.Experience
			fmt.Printf("\nExperience %s\n", e.ID)
			fmt.Printf("  evaluación: tool=%.2f contrato=%.2f goal=%.2f world=%.2f -> final=%.2f\n",
				e.Evaluation.ToolScore, e.Evaluation.ContractScore, e.Evaluation.GoalScore,
				e.Evaluation.WorldScore, e.Evaluation.FinalScore)
			fmt.Printf("  certeza: %.3f -> %.3f (Δ %+.3f)\n", e.PreviousConfidence, e.ResultConfidence, e.ConfidenceDelta)
			if dir := rt.PersistenceDir(); dir != "" {
				fmt.Printf("  memoria: %s\n", dir)
			}
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().StringVar(&goal, "goal", "", "El objetivo a resolver (obligatorio)")
	cmd.Flags().StringVar(&intent, "intent", "", "Nombre del intent (ej. search_series) — por default, el propio goal")
	cmd.Flags().StringVar(&agent, "agent", "", "Nombre de variable del AGCA.agent(...) cuyos permisos aplicar — sin él, la unión de todos")
	cmd.Flags().StringVar(&role, "role", "", "Rol con el que se actúa (AGCA.role(...)) — la autoridad efectiva es la intersección de rol y agente")
	cmd.Flags().StringVar(&user, "user", "", "Usuario que pide la acción — su rol se resuelve contra los users=[...] declarados (excluyente con --role)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir la decisión y la experiencia como JSON")
	cmd.Flags().BoolVar(&ephemeral, "ephemeral", false, "No persistir decisiones/experiencias/certeza (esta corrida no recuerda nada)")
	return cmd
}

// graphRolesCmd responde la pregunta operativa central de un sistema con
// roles: "¿qué puede hacer cada rol, exactamente?" — con la cadena de
// herencia ya resuelta, para que no haya que reconstruirla a mano
// leyendo el .asterion.
func graphRolesCmd() *cobra.Command {
	var intelligence string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "roles <archivo.asterion>",
		Short: "Lista los roles declarados y qué capabilities puede invocar cada uno (herencia ya resuelta)",
		Long: "Muestra, por rol, el conjunto EFECTIVO de capabilities tras aplanar su cadena de\n" +
			"herencia. La regla es deny-gana-siempre: si cualquier rol de la cadena deniega una\n" +
			"capability, ningún allow posterior la reabre. Un rol sin allow no puede invocar\n" +
			"nada (deny-by-default).\n\n" +
			"La autoridad con la que se ejecuta algo es la INTERSECCIÓN del rol (en nombre de\n" +
			"quién) y del agente (quién actúa): ninguno de los dos puede ampliar al otro.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := openRuntime(args[0], intelligence, false)
			if err != nil {
				return err
			}
			roles, err := rt.Roles()
			if err != nil {
				return err
			}
			if len(roles) == 0 {
				fmt.Printf("%q no declara ningún AGCA.role(...).\n", rt.Intelligence.Name)
				fmt.Println("Sin roles, la autoridad queda solo en los permisos de cada AGCA.agent(...).")
				return nil
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(roles)
			}
			for _, r := range roles {
				fmt.Printf("%s\n", r.Name)
				if r.Description != "" {
					fmt.Printf("  %s\n", r.Description)
				}
				if len(r.Chain) > 1 {
					fmt.Printf("  hereda: %s\n", strings.Join(r.Chain[:len(r.Chain)-1], " -> "))
				}
				if len(r.Allow) == 0 {
					fmt.Printf("  puede: (nada — deny-by-default)\n")
				} else {
					fmt.Printf("  puede:\n")
					for _, a := range r.Allow {
						fmt.Printf("    ✓ %s\n", a)
					}
				}
				for _, d := range r.Deny {
					fmt.Printf("    ✗ %s (denegado explícitamente)\n", d)
				}
				if len(r.Users) > 0 {
					fmt.Printf("  usuarios: %s\n", strings.Join(r.Users, ", "))
				}
				fmt.Println()
			}
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir los roles resueltos como JSON")
	return cmd
}

func graphDecisionsCmd() *cobra.Command {
	var intelligence string
	var limit int
	cmd := &cobra.Command{
		Use:   "decisions <archivo.asterion>",
		Short: "Lista las decisiones cognitivas ya tomadas por esta inteligencia (persistidas entre corridas)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := openRuntime(args[0], intelligence, true)
			if err != nil {
				return err
			}
			list, err := rt.Decisions.List(context.Background(), limit)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Printf("Todavía no hay ninguna decisión registrada para %q.\n", rt.Intelligence.Name)
				fmt.Printf("Ejecuta 'asterion graph act %s --goal \"...\"' para generar la primera.\n", args[0])
				return nil
			}
			for _, d := range list {
				status := d.SelectedCapability
				if !d.Executed() {
					status = "NO_EXECUTION (" + d.NoExecutionReason + ")"
				}
				fmt.Printf("%s  %s  intent=%s  confianza=%.3f  -> %s\n",
					d.CreatedAt.Format("2006-01-02 15:04:05"), d.ID, d.Intent.Name, d.Confidence, status)
			}
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().IntVar(&limit, "limit", 20, "Cuántas decisiones mostrar (las más recientes)")
	return cmd
}

func graphExplainCmd() *cobra.Command {
	var intelligence string
	cmd := &cobra.Command{
		Use:   "explain <archivo.asterion> <decision-id>",
		Short: "Explica una decisión: candidatos, scores, descartes con motivo, política aplicada y certeza",
		Long: "Responde, para una decisión puntual: ¿qué ejecutaste? ¿por qué? ¿qué contrato lo\n" +
			"autorizó? ¿qué candidatos había y por qué perdieron? Es la contracara del\n" +
			"aprendizaje: AGCA puede cambiar su comportamiento con la experiencia sin perder\n" +
			"la trazabilidad de cada decisión.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := openRuntime(args[0], intelligence, true)
			if err != nil {
				return err
			}
			d, err := rt.Decisions.Get(context.Background(), args[1])
			if err != nil {
				return err
			}
			printDecision(d)
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	return cmd
}

func graphExperienceCmd() *cobra.Command {
	var intelligence string
	var limit int
	cmd := &cobra.Command{
		Use:   "experience <archivo.asterion> [experience-id]",
		Short: "Lista experiencias registradas, o inspecciona una: qué se esperaba, qué pasó y cómo cambió la certeza",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := openRuntime(args[0], intelligence, true)
			if err != nil {
				return err
			}
			store, ok := rt.Experiences.(*agcacognition.FileStore)
			if !ok {
				return fmt.Errorf("esta inteligencia está corriendo sin memoria persistente")
			}
			if len(args) == 2 {
				e, err := store.GetExperience(context.Background(), args[1])
				if err != nil {
					return err
				}
				printExperience(e)
				return nil
			}
			list, err := store.ListExperiences(context.Background(), limit)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Printf("Todavía no hay experiencias registradas para %q.\n", rt.Intelligence.Name)
				return nil
			}
			for _, e := range list {
				outcome := "ok"
				if !e.Succeeded {
					outcome = "falló"
				}
				fmt.Printf("%s  %s  %s  %s  final=%.2f  certeza %.3f -> %.3f (Δ %+.3f)\n",
					e.CreatedAt.Format("2006-01-02 15:04:05"), e.ID, e.SelectedCapability, outcome,
					e.Evaluation.FinalScore, e.PreviousConfidence, e.ResultConfidence, e.ConfidenceDelta)
			}
			return nil
		},
	}
	intelligenceFlag(cmd, &intelligence)
	cmd.Flags().IntVar(&limit, "limit", 20, "Cuántas experiencias mostrar (las más recientes)")
	return cmd
}

func printDecision(d agcacognition.Decision) {
	fmt.Printf("Decision %s\n", d.ID)
	fmt.Printf("  Intent: %s\n", d.Intent.Name)
	if d.Intent.Goal != "" && d.Intent.Goal != d.Intent.Name {
		fmt.Printf("    goal: %s\n", d.Intent.Goal)
	}
	fmt.Printf("  World: %s (%s)\n", d.WorldID, d.WorldStateRef)
	if d.Role != "" {
		if d.User != "" {
			fmt.Printf("  Actor: %s (rol %s)\n", d.User, d.Role)
		} else {
			fmt.Printf("  Rol: %s\n", d.Role)
		}
	}

	var selected, rejected []agcacognition.Candidate
	for _, c := range d.CandidateCapabilities {
		if c.Rejected {
			rejected = append(rejected, c)
		} else {
			selected = append(selected, c)
		}
	}

	if len(selected) > 0 {
		fmt.Println("  Candidatos:")
		for _, c := range selected {
			marker := " "
			if c.CapabilityID == d.SelectedCapability {
				marker = "*"
			}
			fmt.Printf("   %s %-34s %.3f  (goal=%.2f world=%.2f contrato=%.2f experiencia=%.2f)\n",
				marker, c.CapabilityID, c.Score, c.GoalSimilarity, c.WorldSimilarity, c.ContractCompat, c.ExperienceConfidence)
		}
	}
	if len(rejected) > 0 {
		fmt.Println("  Descartados:")
		for _, c := range rejected {
			fmt.Printf("     %-34s %s\n", c.CapabilityID, c.RejectedReason)
		}
	}
	if len(d.PoliciesApplied) > 0 {
		fmt.Println("  Políticas evaluadas:")
		for _, p := range d.PoliciesApplied {
			verdict := "permite"
			if !p.Allowed {
				verdict = "deniega"
			}
			fmt.Printf("     %-20s %s %s %s\n", p.Policy, verdict, p.CapabilityID, p.Reason)
		}
	}
	if d.Executed() {
		fmt.Printf("  Seleccionada: %s\n", d.SelectedCapability)
		fmt.Printf("  Contrato: %s\n", d.ContractRef)
		fmt.Printf("  Confianza: %.3f\n", d.Confidence)
	} else {
		fmt.Printf("  NO_EXECUTION: %s\n", d.NoExecutionReason)
	}
	if len(d.ReasoningTrace.Steps) > 0 {
		fmt.Println("  Traza:")
		for _, s := range d.ReasoningTrace.Steps {
			fmt.Printf("     %s\n", s)
		}
	}
}

func printExperience(e agcacognition.Experience) {
	fmt.Printf("Experience %s\n", e.ID)
	fmt.Printf("  Decision: %s\n", e.DecisionID)
	fmt.Printf("  Capability: %s\n", e.SelectedCapability)
	fmt.Printf("  World: %s -> %s\n", e.WorldBeforeRef, e.WorldAfterRef)
	outcome := "exitosa"
	if !e.Succeeded {
		outcome = "fallida: " + e.ErrorMessage
	}
	fmt.Printf("  Ejecución: %s (%d ms)\n", outcome, e.DurationMS)
	fmt.Printf("  Evaluación:\n")
	fmt.Printf("     tool      %.3f   (autoevaluación de la Tool — nunca decide sola)\n", e.Evaluation.ToolScore)
	fmt.Printf("     contrato  %.3f   (¿cumplió sus guarantees?)\n", e.Evaluation.ContractScore)
	fmt.Printf("     goal      %.3f\n", e.Evaluation.GoalScore)
	fmt.Printf("     world     %.3f\n", e.Evaluation.WorldScore)
	fmt.Printf("     final     %.3f\n", e.Evaluation.FinalScore)
	fmt.Printf("  Certeza: %.3f -> %.3f (Δ %+.3f) en contexto %s\n",
		e.PreviousConfidence, e.ResultConfidence, e.ConfidenceDelta, e.Context.String())
	if keys := e.ContextVector.Keys(); len(keys) > 0 {
		fmt.Printf("  Contexto observado: %s\n", strings.Join(keys, ", "))
	}
}
