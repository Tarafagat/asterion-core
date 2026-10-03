package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"asterion-core/internal/doctor"
	"asterion-core/internal/plugins"
)

// doctorCmd es 'asterion doctor [plugin]': un solo comando para lo que
// antes había que juntar a mano con 'plugin status' + 'plugin services' +
// mirar si el .env quedó commiteado + acordarse si psql estaba instalado.
//
// Sin argumento, dos cosas que no dependen de ningún plugin en particular
// (Environment/AI/Deployment de esta máquina) y una línea por cada plugin
// instalado, la más urgente primero. Con un nombre, el reporte completo de
// ESE plugin: salud de su proceso y de los servicios que declara,
// coincidencia de versión de su lenguaje, y lo que se puede decir de
// verdad sobre seguridad — ver el comentario de paquete en
// internal/doctor: "declarado" nunca se confunde con "forzado".
func doctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor [plugin]",
		Short: "Diagnóstico de salud, entorno, seguridad y despliegue — de un plugin, o de todos",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runDoctorPlugin(args[0], asJSON)
			}
			return runDoctorAll(asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Imprimir el reporte como JSON en vez de texto")
	return cmd
}

func runDoctorPlugin(name string, asJSON bool) error {
	installed, err := plugins.Get(name)
	if err != nil {
		return err
	}
	ctx := context.Background()

	report := doctor.Run(ctx, installed)
	machine := doctor.MachineReport(ctx)

	if asJSON {
		printJSON(struct {
			Plugin  doctor.Report `json:"plugin"`
			Machine doctor.Report `json:"machine"`
		}{report, machine})
		return nil
	}

	fmt.Printf("Diagnóstico de %q\n", installed.Name)
	printSections(report)
	printSections(machine)
	printSummaryLine(append(append([]doctor.Check{}, report.Checks...), machine.Checks...))
	return failIfWorse(report, machine)
}

func runDoctorAll(asJSON bool) error {
	ctx := context.Background()
	machine := doctor.MachineReport(ctx)

	lines, err := doctor.RunAll(ctx)
	if err != nil {
		return err
	}

	if asJSON {
		printJSON(struct {
			Machine any `json:"machine"`
			Plugins any `json:"plugins_summary"`
		}{Machine: machine, Plugins: lines})
		return nil
	}

	printSections(machine)
	fmt.Println()

	if len(lines) == 0 {
		fmt.Println("Ningún plugin instalado todavía — 'asterion plugin install <repo>'")
		return nil
	}
	fmt.Println("Plugins instalados")
	for _, l := range lines {
		marker := l.Worst.Marker()
		if l.Issue == "" {
			fmt.Printf(" %s %-24s todo bien\n", marker, l.Plugin)
		} else {
			fmt.Printf(" %s %-24s %s\n", marker, l.Plugin, l.Issue)
		}
	}
	fmt.Printf("\nPara el detalle completo de uno: asterion doctor <plugin>\n")

	for _, l := range lines {
		if l.Worst >= doctor.Fail {
			return fmt.Errorf("al menos un plugin tiene un problema grave (%s)", l.Plugin)
		}
	}
	return nil
}

func printSections(r doctor.Report) {
	for _, section := range r.BySection() {
		fmt.Printf("\n%s\n", section)
		for _, c := range r.ChecksIn(section) {
			fmt.Printf(" %s %-14s %s\n", c.Severity.Marker(), c.Name, c.Detail)
		}
	}
}

func printSummaryLine(checks []doctor.Check) {
	var ok, warn, fail int
	for _, c := range checks {
		switch c.Severity {
		case doctor.OK:
			ok++
		case doctor.Warn:
			warn++
		case doctor.Fail:
			fail++
		}
	}
	fmt.Printf("\n%d ok, %d warn, %d fail\n", ok, warn, fail)
}

func failIfWorse(reports ...doctor.Report) error {
	var worst []string
	for _, r := range reports {
		if r.Worst() >= doctor.Fail {
			for _, c := range r.Checks {
				if c.Severity == doctor.Fail {
					worst = append(worst, c.Name+": "+c.Detail)
				}
			}
		}
	}
	if len(worst) == 0 {
		return nil
	}
	return fmt.Errorf("%d problema(s) grave(s):\n  - %s", len(worst), strings.Join(worst, "\n  - "))
}
