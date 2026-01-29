package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// OuterConfig represents the top-level YAML structure
type OuterConfig struct {
	Version    string      `yaml:"version"`
	ConfigMaps []ConfigMap `yaml:"configmaps"`
}

// ConfigMap represents a single configmap entry
type ConfigMap struct {
	Name      string            `yaml:"name"`
	Region    string            `yaml:"region"`
	Namespace string            `yaml:"namespace"`
	Data      map[string]string `yaml:"data"`
}

// JacksonConfig represents the jackson configuration
type JacksonConfig struct {
	DefaultPropertyInclusion string `yaml:"default-property-inclusion"`
}

// SpringConfig represents the spring configuration
type SpringConfig struct {
	Jackson JacksonConfig `yaml:"jackson"`
}

func main() {
	// Directories to process
	dirs := []string{
		"dev/configmaps",
		"cert-dev/configmaps",
		"pre/configmaps",
		"pro/configmaps",
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

	// Find all YAML files in the directory
	files, err := filepath.Glob(filepath.Join(dirPath, "*.yaml"))
	if err != nil {
		return err
	}

	ymlFiles, err := filepath.Glob(filepath.Join(dirPath, "*.yml"))
	if err != nil {
		return err
	}
	files = append(files, ymlFiles...)

	for _, file := range files {
		fmt.Printf("Processing file: %s\n", file)
		if err := processFile(file); err != nil {
			fmt.Printf("  Error processing file %s: %v\n", file, err)
		} else {
			fmt.Printf("  Successfully processed: %s\n", file)
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

	// Parse the outer YAML structure
	var config OuterConfig
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	modified := false

	// Process each configmap
	for i := range config.ConfigMaps {
		cm := &config.ConfigMaps[i]

		// Process each data entry
		for key, value := range cm.Data {
			if strings.HasSuffix(key, ".yaml") || strings.HasSuffix(key, ".yml") {
				updatedValue, wasModified, err := addSpringJacksonConfig(value)
				if err != nil {
					fmt.Printf("    Warning: Could not process embedded YAML in %s: %v\n", key, err)
					continue
				}
				if wasModified {
					cm.Data[key] = updatedValue
					modified = true
					fmt.Printf("    Modified embedded config in: %s (region: %s)\n", key, cm.Region)
				}
			}
		}
	}

	if !modified {
		fmt.Printf("  No modifications needed\n")
		return nil
	}

	// Marshal back to YAML with proper formatting
	output, err := marshalWithPreservedFormat(&config)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	// Write back to file
	if err := os.WriteFile(filePath, output, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func addSpringJacksonConfig(embeddedYAML string) (string, bool, error) {
	// Parse the embedded YAML as a generic map to preserve structure
	var data yaml.Node
	if err := yaml.Unmarshal([]byte(embeddedYAML), &data); err != nil {
		return embeddedYAML, false, fmt.Errorf("failed to parse embedded YAML: %w", err)
	}

	// Handle empty content
	if data.Kind == 0 {
		// Empty document, create new structure
		newYAML := "\nspring:\n  jackson:\n    default-property-inclusion: non_null\n"
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

	result := buf.String()
	// Ensure it starts with newline to match original format
	if !strings.HasPrefix(result, "\n") {
		result = "\n" + result
	}

	return result, true, nil
}

func marshalWithPreservedFormat(config *OuterConfig) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("---\n")

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(config); err != nil {
		return nil, err
	}
	encoder.Close()

	return []byte(buf.String()), nil
}
