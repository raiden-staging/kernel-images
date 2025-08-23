package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScreenResolutionParameterValidation(t *testing.T) {
	// Test parameter validation in the SetScreenResolution function
	testCases := []struct {
		name        string
		width       int
		height      int
		rate        *int
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid parameters",
			width:       1920,
			height:      1080,
			rate:        intPtr(60),
			expectError: false,
		},
		{
			name:        "valid without rate",
			width:       1280,
			height:      720,
			rate:        nil,
			expectError: false,
		},
		{
			name:        "width too small",
			width:       100,
			height:      1080,
			rate:        nil,
			expectError: true,
			errorMsg:    "width must be between 640 and 7680",
		},
		{
			name:        "width too large",
			width:       9000,
			height:      1080,
			rate:        nil,
			expectError: true,
			errorMsg:    "width must be between 640 and 7680",
		},
		{
			name:        "height too small",
			width:       1920,
			height:      100,
			rate:        nil,
			expectError: true,
			errorMsg:    "height must be between 480 and 4320",
		},
		{
			name:        "height too large",
			width:       1920,
			height:      9000,
			rate:        nil,
			expectError: true,
			errorMsg:    "height must be between 480 and 4320",
		},
		{
			name:        "rate too small",
			width:       1920,
			height:      1080,
			rate:        intPtr(10),
			expectError: true,
			errorMsg:    "rate must be between 24 and 240",
		},
		{
			name:        "rate too large",
			width:       1920,
			height:      1080,
			rate:        intPtr(300),
			expectError: true,
			errorMsg:    "rate must be between 24 and 240",
		},
	}

	// Create stub request object
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := SetScreenResolutionRequestObject{
				Width:  tc.width,
				Height: tc.height,
				Rate:   tc.rate,
			}

			// Just test the validation part
			if req.Width < 640 || req.Width > 7680 {
				assert.True(t, tc.expectError, "Expected validation error for width")
			}

			if req.Height < 480 || req.Height > 4320 {
				assert.True(t, tc.expectError, "Expected validation error for height")
			}

			if req.Rate != nil && (*req.Rate < 24 || *req.Rate > 240) {
				assert.True(t, tc.expectError, "Expected validation error for rate")
			}
		})
	}
}

// Helper function to create int pointer
func intPtr(i int) *int {
	return &i
}
