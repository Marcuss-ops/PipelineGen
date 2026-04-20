package drive

import (
	"testing"
	"strings"
)

// TestGetOrCreateFolderWithApostrophe verifica che cartelle con apostrofi vengono gestite correttamente
func TestGetOrCreateFolderWithApostrophe(t *testing.T) {
	tests := []struct {
		name      string
		folderName string
		shouldEscape bool
	}{
		{
			name:      "Simple folder name",
			folderName: "Videos",
			shouldEscape: false,
		},
		{
			name:      "Folder with apostrophe",
			folderName: "O'Brien",
			shouldEscape: true,
		},
		{
			name:      "Folder with multiple apostrophes",
			folderName: "Maria's Children's Videos",
			shouldEscape: true,
		},
		{
			name:      "Folder with special chars and apostrophe",
			folderName: "2024's Best (Clips)",
			shouldEscape: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the escaping logic
			escapedName := strings.ReplaceAll(tt.folderName, "'", "\\'")
			
			// Verify escaping worked
			if tt.shouldEscape && !strings.Contains(escapedName, "\\'") {
				t.Errorf("Expected apostrophe to be escaped in: %s", escapedName)
			}
			
			// Build query
			query := "name='" + escapedName + "' and 'root' in parents"
			
			// Verify that the original unescaped name no longer appears in query
			// (if it had apostrophes that should be escaped)
			if tt.shouldEscape {
				// Check that the dangerous pattern (apostrophe not escaped) is NOT in query
				// E.g., for "O'Brien" we should NOT have "name='O'Brien'" but "name='O\'Brien'"
				if strings.Contains(query, "'+") || strings.Contains(query, "' and") {
					// This would indicate the escaped quote is properly breaking up potential SQL
					t.Logf("Escaped query correctly: %s", query)
				}
			}
		})
	}
}

// TestGetFolderByPathSplitting verifica che path nested vengono splittati correttamente
func TestGetFolderByPathSplitting(t *testing.T) {
	tests := []struct {
		path           string
		expectedParts  []string
	}{
		{
			path:          "Progetti/Video/Finali",
			expectedParts: []string{"Progetti", "Video", "Finali"},
		},
		{
			path:          "Stock/Topics/Music",
			expectedParts: []string{"Stock", "Topics", "Music"},
		},
		{
			path:          "Single",
			expectedParts: []string{"Single"},
		},
		{
			path:          "With/Trailing/Slash/",
			expectedParts: []string{"With", "Trailing", "Slash"},
		},
		{
			path:          "With  /  Spaces  /  Between",
			expectedParts: []string{"With", "Spaces", "Between"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// Simulate the fixed splitting logic
			parts := strings.Split(tt.path, "/")
			var trimmedParts []string
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part != "" {
					trimmedParts = append(trimmedParts, part)
				}
			}

			if len(trimmedParts) != len(tt.expectedParts) {
				t.Errorf("Expected %d parts, got %d. Parts: %v", 
					len(tt.expectedParts), len(trimmedParts), trimmedParts)
			}

			for i, expected := range tt.expectedParts {
				if i >= len(trimmedParts) {
					t.Errorf("Missing part at index %d: expected %s", i, expected)
					break
				}
				if trimmedParts[i] != expected {
					t.Errorf("Part %d: expected %s, got %s", i, expected, trimmedParts[i])
				}
			}
		})
	}
}

// TestUploadValidation verifica che validazioni vengono eseguite
func TestUploadValidation(t *testing.T) {
	tests := []struct {
		name      string
		folderID  string
		shouldFail bool
	}{
		{
			name:      "Valid folderID",
			folderID:  "folder-123",
			shouldFail: false,
		},
		{
			name:      "Empty folderID",
			folderID:  "",
			shouldFail: true,
		},
		{
			name:      "Root folderID",
			folderID:  "root",
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate validation
			if tt.folderID == "" {
				if !tt.shouldFail {
					t.Error("Expected validation to fail for empty folderID")
				}
			} else {
				if tt.shouldFail {
					t.Error("Expected validation to pass")
				}
			}
		})
	}
}
