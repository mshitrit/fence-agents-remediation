package validation

import (
	"testing"

	"k8s.io/apimachinery/pkg/version"
)

func Test_setOutOfTaintSupportedFlag(t *testing.T) {
	type args struct {
		version *version.Info
	}
	tests := []struct {
		name                    string
		args                    args
		wantErr                 bool
		isOutOfTaintFlagEnabled bool
	}{
		//valid use-cases
		{name: "validEnabledNoPlus", args: args{&version.Info{Major: "1", Minor: "26"}}, wantErr: false, isOutOfTaintFlagEnabled: true},
		{name: "validDisabledEnabledNoPlus", args: args{&version.Info{Major: "1", Minor: "24"}}, wantErr: false, isOutOfTaintFlagEnabled: false},
		{name: "validEnabledWithPlus", args: args{&version.Info{Major: "1", Minor: "26+"}}, wantErr: false, isOutOfTaintFlagEnabled: true},
		{name: "validDisabledWithPlus", args: args{&version.Info{Major: "1", Minor: "24+"}}, wantErr: false, isOutOfTaintFlagEnabled: false},
		{name: "validEnabledWithTrailingChars", args: args{&version.Info{Major: "1", Minor: "26.5.2#$%+"}}, wantErr: false, isOutOfTaintFlagEnabled: true},
		{name: "validDisabledWithTrailingChars", args: args{&version.Info{Major: "1", Minor: "22.5.2#$%+"}}, wantErr: false, isOutOfTaintFlagEnabled: false},

		//invalid use-cases
		{name: "inValidNoPlus", args: args{&version.Info{Major: "1", Minor: "%24"}}, wantErr: true, isOutOfTaintFlagEnabled: false},
		{name: "inValidWithPlus", args: args{&version.Info{Major: "1+", Minor: "26"}}, wantErr: true, isOutOfTaintFlagEnabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OutOfServiceTaintValidator{}
			if err := v.setOutOfServiceTaintSupportedFlag(tt.args.version); (err != nil) != tt.wantErr || v.IsOutOfServiceTaintSupported() != tt.isOutOfTaintFlagEnabled {
				t.Errorf("setOutOfTaintFlags() error = %v, wantErr %v, expected out of taint flag %v", err, tt.wantErr, tt.isOutOfTaintFlagEnabled)
			}
		})
	}
}

func TestValidateActionParameter(t *testing.T) {
	tests := []struct {
		name        string
		paramName   string
		paramValue  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid action reboot",
			paramName:   "action",
			paramValue:  "reboot",
			expectError: false,
		},
		{
			name:        "valid action empty",
			paramName:   "action",
			paramValue:  "",
			expectError: false,
		},
		{
			name:        "valid alt action reboot",
			paramName:   "--action",
			paramValue:  "reboot",
			expectError: false,
		},
		{
			name:        "valid alt action empty",
			paramName:   "--action",
			paramValue:  "",
			expectError: false,
		},
		{
			name:        "invalid action status",
			paramName:   "action",
			paramValue:  "status",
			expectError: true,
			errorMsg:    "action parameter 'action' must be 'reboot' or empty, got 'status'",
		},
		{
			name:        "invalid action off",
			paramName:   "action",
			paramValue:  "off",
			expectError: true,
			errorMsg:    "action parameter 'action' must be 'reboot' or empty, got 'off'",
		},
		{
			name:        "invalid alt action off",
			paramName:   "--action",
			paramValue:  "off",
			expectError: true,
			errorMsg:    "action parameter '--action' must be 'reboot' or empty, got 'off'",
		},
		{
			name:        "non-action parameter",
			paramName:   "ip",
			paramValue:  "192.168.1.100",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateActionParameter(tt.paramName, tt.paramValue)
			if tt.expectError {
				if err == nil {
					t.Errorf("ValidateActionParameter() expected error but got none")
				} else if err.Error() != tt.errorMsg {
					t.Errorf("ValidateActionParameter() error = %v, expected %v", err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateActionParameter() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestValidateParameterConsistency(t *testing.T) {
	tests := []struct {
		name         string
		sharedParams map[string]string
		nodeParams   map[string]map[string]string
		expectErrors int
		errorMsgs    []string
	}{
		{
			name: "valid parameters",
			sharedParams: map[string]string{
				"ip":       "192.168.1.100",
				"username": "admin",
			},
			nodeParams: map[string]map[string]string{
				"slot": {
					"worker-1": "1",
					"worker-2": "2",
				},
			},
			expectErrors: 0,
		},
		{
			name: "empty shared parameter - allowed",
			sharedParams: map[string]string{
				"ip":       "", // Empty shared parameters are now allowed (values might come from secrets)
				"username": "admin",
			},
			nodeParams:   map[string]map[string]string{},
			expectErrors: 0,
		},
		{
			name:         "empty node parameter map",
			sharedParams: map[string]string{},
			nodeParams: map[string]map[string]string{
				"slot": {},
			},
			expectErrors: 1,
			errorMsgs:    []string{"node parameter 'slot' is defined but has no node mappings"},
		},
		{
			name:         "empty node name",
			sharedParams: map[string]string{},
			nodeParams: map[string]map[string]string{
				"slot": {
					"":         "1",
					"worker-1": "2",
				},
			},
			expectErrors: 1,
			errorMsgs:    []string{"empty node name found in parameter 'slot'"},
		},
		{
			name:         "empty parameter value",
			sharedParams: map[string]string{},
			nodeParams: map[string]map[string]string{
				"slot": {
					"worker-1": "",
					"worker-2": "2",
				},
			},
			expectErrors: 1,
			errorMsgs:    []string{"empty parameter value for node 'worker-1' in parameter 'slot'"},
		},
		{
			name: "multiple errors",
			sharedParams: map[string]string{
				"ip":       "", // Empty shared params are now allowed
				"username": "",
			},
			nodeParams: map[string]map[string]string{
				"slot": {},
				"port": {
					"": "1",
				},
			},
			expectErrors: 2, // Only the node parameter errors remain
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidateParameterConsistency(tt.sharedParams, tt.nodeParams)
			if len(errors) != tt.expectErrors {
				t.Errorf("ValidateParameterConsistency() returned %d errors, expected %d", len(errors), tt.expectErrors)
			}

			if tt.expectErrors > 0 && len(tt.errorMsgs) > 0 {
				for _, expectedMsg := range tt.errorMsgs {
					found := false
					for _, err := range errors {
						if err.Error() == expectedMsg {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("ValidateParameterConsistency() missing expected error: %s", expectedMsg)
					}
				}
			}
		})
	}
}

func TestFenceAgentParameterValidator_ValidateParametersWithStatus(t *testing.T) {
	validator := NewFenceAgentParameterValidator()

	tests := []struct {
		name           string
		agent          string
		parameters     map[string]string
		expectValid    bool
		expectErrors   int
		expectWarnings int
	}{
		{
			name:           "empty agent name",
			agent:          "",
			parameters:     map[string]string{},
			expectValid:    false,
			expectErrors:   1,
			expectWarnings: 0,
		},
		{
			name:  "valid agent with basic parameters",
			agent: "fence_ipmilan",
			parameters: map[string]string{
				"ip":       "192.168.1.100",
				"username": "admin",
			},
			expectValid:    true, // Will likely fail due to connectivity, but that's a warning
			expectErrors:   0,
			expectWarnings: 1, // Connectivity failure
		},
		{
			name:  "exclude action parameters",
			agent: "fence_ipmilan",
			parameters: map[string]string{
				"action":   "reboot", // Should be excluded from status command
				"--action": "reboot", // Should be excluded from status command
				"ip":       "192.168.1.100",
			},
			expectValid:    true,
			expectErrors:   0,
			expectWarnings: 1, // Connectivity failure
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validator.ValidateParametersWithStatus(tt.agent, tt.parameters)
			if err != nil {
				t.Errorf("ValidateParametersWithStatus() unexpected error = %v", err)
				return
			}

			if result.IsValid != tt.expectValid {
				t.Errorf("ValidateParametersWithStatus() IsValid = %v, expected %v", result.IsValid, tt.expectValid)
			}

			if len(result.Errors) != tt.expectErrors {
				t.Errorf("ValidateParametersWithStatus() got %d errors, expected %d", len(result.Errors), tt.expectErrors)
			}

			if len(result.Warnings) != tt.expectWarnings {
				t.Errorf("ValidateParametersWithStatus() got %d warnings, expected %d", len(result.Warnings), tt.expectWarnings)
			}
		})
	}
}
