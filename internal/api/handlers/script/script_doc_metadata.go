package script

import (
	"fmt"

	"strings"
)

func renderMetadata(req ScriptDocsRequest) string {
	var b strings.Builder
	b.WriteString("Topic: ")
	b.WriteString(req.Topic)
	b.WriteString("\nDuration: ")
	b.WriteString(fmt.Sprintf("%d seconds", req.Duration))
	b.WriteString("\nLanguage: ")
	b.WriteString(req.Language)
	b.WriteString("\nTemplate: ")
	b.WriteString(req.Template)
	b.WriteString("\nMode: modular")
	return b.String()
}
