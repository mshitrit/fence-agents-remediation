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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTemplate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Template Utils Suite")
}

var _ = Describe("Template Processing", func() {
	Describe("RenderParameterTemplate", func() {
		It("should replace NodeName placeholder", func() {
			paramValue := "/redfish/v1/Systems/{{.NodeName}}"
			nodeName := "worker-1"

			result, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("/redfish/v1/Systems/worker-1"))
		})

		It("should handle multiple placeholders", func() {
			paramValue := "https://{{.NodeName}}.example.com/{{.NodeName}}/power"
			nodeName := "worker-1"

			result, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("https://worker-1.example.com/worker-1/power"))
		})

		It("should return error for non existing template", func() {
			paramValue := "/redfish/v1/Systems/{{.WrongTemplate}}"
			nodeName := "worker-1"

			_, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("can't evaluate field WrongTemplate"))
		})

		It("should return error for empty node name", func() {
			paramValue := "/redfish/v1/Systems/{{.NodeName}}"
			nodeName := ""

			_, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nodeName cannot be empty"))
		})

		It("should return error for invalid template syntax", func() {
			paramValue := "/redfish/v1/Systems/{{.NodeName"
			nodeName := "worker-1"

			_, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse template"))
		})

		It("should handle strings without templates", func() {
			paramValue := "/redfish/v1/Systems/static-value"
			nodeName := "worker-1"

			result, err := RenderParameterTemplate(paramValue, nodeName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("/redfish/v1/Systems/static-value"))
		})
	})
})
