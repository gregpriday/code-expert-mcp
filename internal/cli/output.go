package cli

import (
	"fmt"
	"os"

	"github.com/gregpriday/codeexpert/internal/schema"
)

// emit writes the markdown and/or JSON rendering to stdout or a file according
// to the requested format.
func emit(markdown, jsonText, format, outputPath string) error {
	var content string
	switch format {
	case "json":
		content = jsonText
	case "both":
		content = markdown + "\n\n```json\n" + jsonText + "\n```\n"
	default: // markdown
		content = markdown
	}
	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return schema.NewError(schema.CodeInternal, "cannot write output file: %v", err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", outputPath)
		return nil
	}
	fmt.Println(content)
	return nil
}
