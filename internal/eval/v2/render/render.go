package render

import (
	"embed"
	"fmt"
	"html/template"
	"io"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

//go:embed template.html
var templateFiles embed.FS

func Report(run v2.RunRecord, baselineArm string, results []v2.TrialResult, writer io.Writer) error {
	tmpl, err := template.New("template.html").ParseFS(templateFiles, "template.html")
	if err != nil {
		return fmt.Errorf("parse eval report template: %w", err)
	}
	if err := tmpl.Execute(writer, buildReportData(run, baselineArm, results)); err != nil {
		return fmt.Errorf("execute eval report template: %w", err)
	}
	return nil
}
