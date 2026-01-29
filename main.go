package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	// Directories to process
	dirs := []string{
		"templates/clients",
		"templates/connectors",
		"templates/other",
		"templates/pbcs",
		"templates/workers",
	}

	// Get base directory from command line argument or use current directory
	baseDir := "."
	if len(os.Args) > 1 {
		baseDir = os.Args[1]
	}

	for _, dir := range dirs {
		fullPath := filepath.Join(baseDir, dir)
		if err := processDirectory(fullPath); err != nil {
			fmt.Printf("Warning: Could not process directory %s: %v\n", fullPath, err)
		}
	}

	fmt.Println("Processing complete!")
}

func processDirectory(dirPath string) error {
	// Check if directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return fmt.Errorf("directory does not exist: %s", dirPath)
	}

	// Find all subdirectories (application folders)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			configMapPath := filepath.Join(dirPath, entry.Name(), "configmap.yaml")
			if _, err := os.Stat(configMapPath); err == nil {
				fmt.Printf("Processing file: %s\n", configMapPath)
				if err := processFile(configMapPath); err != nil {
					fmt.Printf("  Error processing file %s: %v\n", configMapPath, err)
				} else {
					fmt.Printf("  Successfully processed: %s\n", configMapPath)
				}
			}
		}
	}

	return nil
}

func processFile(filePath string) error {
	// Read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Parse as yaml.Node to preserve structure
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("invalid YAML document structure")
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("root is not a mapping")
	}

	// Find the data key
	dataIndex := -1
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "data" {
			dataIndex = i
			break
		}
	}

	if dataIndex < 0 {
		return fmt.Errorf("no 'data' key found")
	}

	dataNode := root.Content[dataIndex+1]
	if dataNode.Kind != yaml.MappingNode {
		return fmt.Errorf("'data' is not a mapping")
	}

	modified := false

	// Process each entry in data
	for i := 0; i < len(dataNode.Content); i += 2 {
		keyNode := dataNode.Content[i]
		valueNode := dataNode.Content[i+1]

		// Check if this is a string (embedded YAML)
		if valueNode.Kind == yaml.ScalarNode && (valueNode.Style == yaml.LiteralStyle || valueNode.Style == yaml.FoldedStyle || valueNode.Tag == "!!str") {
			updatedValue, wasModified, err := addSpringJacksonConfig(valueNode.Value)
			if err != nil {
				fmt.Printf("    Warning: Could not process embedded YAML in %s: %v\n", keyNode.Value, err)
				continue
			}
			if wasModified {
				valueNode.Value = updatedValue
				valueNode.Style = yaml.LiteralStyle // Use |- style
				modified = true
				fmt.Printf("    Modified embedded config in: %s\n", keyNode.Value)
			}
		}
	}

	if !modified {
		fmt.Printf("  No modifications needed\n")
		return nil
	}

	// Marshal back to YAML
	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	encoder.Close()

	// Write back to file
	if err := os.WriteFile(filePath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func addSpringJacksonConfig(embeddedYAML string) (string, bool, error) {
	// Trim leading whitespace for parsing
	trimmedYAML := strings.TrimLeft(embeddedYAML, "\n\r\t ")

	// Parse the embedded YAML as a generic map to preserve structure
	var data yaml.Node
	if err := yaml.Unmarshal([]byte(trimmedYAML), &data); err != nil {
		return embeddedYAML, false, fmt.Errorf("failed to parse embedded YAML: %w", err)
	}

	// Handle empty content
	if data.Kind == 0 {
		// Empty document, create new structure
		newYAML := "spring:\n  jackson:\n    default-property-inclusion: non_null\n"
		return newYAML, true, nil
	}

	// Ensure we're working with a mapping
	if data.Kind != yaml.DocumentNode || len(data.Content) == 0 {
		return embeddedYAML, false, nil
	}

	root := data.Content[0]
	if root.Kind != yaml.MappingNode {
		return embeddedYAML, false, nil
	}

	// Look for existing spring key
	springIndex := -1
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "spring" {
			springIndex = i
			break
		}
	}

	if springIndex >= 0 {
		// spring key exists, add jackson under it
		springValue := root.Content[springIndex+1]

		if springValue.Kind != yaml.MappingNode {
			// spring value is not a mapping, can't add jackson
			return embeddedYAML, false, nil
		}

		// Check if jackson already exists under spring
		for i := 0; i < len(springValue.Content); i += 2 {
			if springValue.Content[i].Value == "jackson" {
				// jackson already exists, check if default-property-inclusion exists
				jacksonValue := springValue.Content[i+1]
				if jacksonValue.Kind == yaml.MappingNode {
					for j := 0; j < len(jacksonValue.Content); j += 2 {
						if jacksonValue.Content[j].Value == "default-property-inclusion" {
							// Already has the config
							return embeddedYAML, false, nil
						}
					}
					// Add default-property-inclusion to existing jackson
					jacksonValue.Content = append(jacksonValue.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "default-property-inclusion"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: "non_null"},
					)
				}
				goto marshal
			}
		}

		// jackson doesn't exist, add it to spring
		jacksonNode := &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "default-property-inclusion"},
				{Kind: yaml.ScalarNode, Value: "non_null"},
			},
		}
		springValue.Content = append(springValue.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "jackson"},
			jacksonNode,
		)
	} else {
		// spring key doesn't exist, add it
		springNode := &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "jackson"},
				{
					Kind: yaml.MappingNode,
					Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Value: "default-property-inclusion"},
						{Kind: yaml.ScalarNode, Value: "non_null"},
					},
				},
			},
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "spring"},
			springNode,
		)
	}

marshal:
	// Marshal back to YAML
	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&data); err != nil {
		return embeddedYAML, false, fmt.Errorf("failed to marshal embedded YAML: %w", err)
	}
	encoder.Close()

	result := strings.TrimLeft(buf.String(), "\n\r\t ")

	return result, true, nil
}

