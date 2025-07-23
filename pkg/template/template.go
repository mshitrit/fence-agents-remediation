/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package template

import (
	"bytes"
	"fmt"
	"text/template"
)

// TemplateData contains the data available for template substitution
type TemplateData struct {
	NodeName string `json:"nodeName"`
}

// RenderParameterTemplate processes a parameter value string with Go template syntax
// It replaces placeholders like {{.NodeName}} with actual values
func RenderParameterTemplate(paramValue string, nodeName string) (string, error) {
	if nodeName == "" {
		return "", fmt.Errorf("nodeName cannot be empty")
	}

	// Create template data
	data := TemplateData{
		NodeName: nodeName,
	}

	// Process the parameter value with Go templates
	return processTemplateString(paramValue, data)
}

// processTemplateString processes a string with Go template syntax
func processTemplateString(templateStr string, data TemplateData) (string, error) {
	tmpl, err := template.New("far-template").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
