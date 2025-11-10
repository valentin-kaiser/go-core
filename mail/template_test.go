package mail_test

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/mail"
)

func TestTemplateManager_Comprehensive(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "default.html",
		AutoReload:      true,
	}

	tm := mail.NewTemplateManager(config)
	if tm == nil {
		t.Fatal("Expected TemplateManager to be created")
	}
}

func TestTemplateManager_WithTemplateFunc(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)

	// Test adding custom template function
	result := tm.WithTemplateFunc("uppercase", func(s string) string {
		return s
	})

	if result != tm {
		t.Error("Expected WithTemplateFunc to return same instance")
	}

	// Test adding duplicate function should return error
	tm.WithTemplateFunc("uppercase", func(s string) string {
		return s
	})

	if tm.Error == nil {
		t.Error("Expected error when adding duplicate template function")
	}
}

func TestTemplateManager_WithDefaultFuncs(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)
	result := tm.WithDefaultFuncs()

	if result != tm {
		t.Error("Expected WithDefaultFuncs to return same instance")
	}

	// Test that default functions are available by attempting to render
	// a template that uses them (this would be caught during parsing)
}

func TestTemplateManager_LoadTemplate_NoSource(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)

	// Should fail when no filesystem or path is configured
	_, err := tm.LoadTemplate("test.html")
	if err == nil {
		t.Error("Expected error when loading template without configured source")
	}
}

func TestTemplateManager_RenderTemplate_NoSource(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)

	// Should fail when no template is loaded
	_, err := tm.RenderTemplate("test.html", map[string]string{"name": "test"})
	if err == nil {
		t.Error("Expected error when rendering non-existent template")
	}
}

func TestTemplateManager_RenderTemplateContent(t *testing.T) {
	config := mail.TemplateConfig{
		WithDefaultFuncs: true,
	}

	tm := mail.NewTemplateManager(config)

	templateContent := "<h1>Hello {{.Name}}</h1><p>Welcome to {{.Company}}!</p>"
	data := map[string]interface{}{
		"Name":    "John Doe",
		"Company": "Example Corp",
	}

	result, err := tm.RenderTemplateContent(templateContent, data)
	if err != nil {
		t.Errorf("Expected no error when rendering template content, got: %v", err)
	}

	expected := "<h1>Hello John Doe</h1><p>Welcome to Example Corp!</p>"
	if result != expected {
		t.Errorf("Expected rendered template to be '%s', got '%s'", expected, result)
	}
}

func TestTemplateManager_RenderTemplateContent_EmptyContent(t *testing.T) {
	config := mail.TemplateConfig{}
	tm := mail.NewTemplateManager(config)

	_, err := tm.RenderTemplateContent("", map[string]string{"name": "test"})
	if err == nil {
		t.Error("Expected error when rendering empty template content")
	}
}

func TestTemplateManager_RenderTemplateContent_WithCustomFuncs(t *testing.T) {
	config := mail.TemplateConfig{}
	tm := mail.NewTemplateManager(config)

	templateContent := "<h1>{{uppercase .Name}}</h1>"
	data := map[string]interface{}{
		"Name": "john doe",
	}

	customFuncs := template.FuncMap{
		"uppercase": func(s string) string {
			return strings.ToUpper(s)
		},
	}

	result, err := tm.RenderTemplateContent(templateContent, data, customFuncs)
	if err != nil {
		t.Errorf("Expected no error when rendering template content with custom funcs, got: %v", err)
	}

	expected := "<h1>JOHN DOE</h1>"
	if result != expected {
		t.Errorf("Expected rendered template to be '%s', got '%s'", expected, result)
	}
}

func TestTemplateManager_WithFileServer_InvalidPath(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)
	result := tm.WithFileServer("/nonexistent/path")

	if result != tm {
		t.Error("Expected WithFileServer to return same instance")
	}

	if tm.Error == nil {
		t.Error("Expected error when setting invalid file server path")
	}
}

func TestTemplateManager_WithFileServer_ValidPath(t *testing.T) {
	// Create a temporary directory with templates
	tempDir := t.TempDir()

	// Create a test template file
	templateContent := `<html><body><h1>{{.Title}}</h1><p>{{.Content}}</p></body></html>`
	templatePath := filepath.Join(tempDir, "test.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write test template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)
	result := tm.WithFileServer(tempDir)

	if result != tm {
		t.Error("Expected WithFileServer to return same instance")
	}

	if tm.Error != nil {
		t.Errorf("Unexpected error: %v", tm.Error)
	}

	// Test loading the template
	template, err := tm.LoadTemplate("test.html")
	if err != nil {
		t.Errorf("Failed to load template: %v", err)
	}

	if template == nil {
		t.Error("Expected non-nil template")
	}

	// Test rendering the template
	data := map[string]string{
		"Title":   "Test Title",
		"Content": "Test Content",
	}

	rendered, err := tm.RenderTemplate("test.html", data)
	if err != nil {
		t.Errorf("Failed to render template: %v", err)
	}

	if rendered == "" {
		t.Error("Expected non-empty rendered template")
	}

	// Check that rendered content contains expected data
	if !containsString(rendered, "Test Title") {
		t.Errorf("Rendered template should contain title, got: %s", rendered)
	}

	if !containsString(rendered, "Test Content") {
		t.Errorf("Rendered template should contain content, got: %s", rendered)
	}
}

func TestTemplateManager_WithFS(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(config)
	result := tm.WithFS(nil) // Test with nil filesystem

	if result != tm {
		t.Error("Expected WithFS to return same instance")
	}

	// Note: We can't fully test this without actual embedded templates
	// but we can verify the method works
}

func TestTemplateManager_AutoReload(t *testing.T) {
	// Create a temporary directory with templates
	tempDir := t.TempDir()

	// Create initial template
	templateContent := `<html><body>{{.Message}}</body></html>`
	templatePath := filepath.Join(tempDir, "reload.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write initial template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "reload.html",
		AutoReload:      true,
	}

	tm := mail.NewTemplateManager(config).WithFileServer(tempDir)

	if tm.Error != nil {
		t.Fatalf("Unexpected error setting up template manager: %v", tm.Error)
	}

	// Load initial template
	_, err := tm.LoadTemplate("reload.html")
	if err != nil {
		t.Errorf("Failed to load initial template: %v", err)
	}

	// Render initial template
	data := map[string]string{"Message": "Initial"}
	rendered, err := tm.RenderTemplate("reload.html", data)
	if err != nil {
		t.Errorf("Failed to render initial template: %v", err)
	}

	if !containsString(rendered, "Initial") {
		t.Errorf("Initial render should contain 'Initial', got: %s", rendered)
	}

	// Update the template file
	updatedContent := `<html><body><h1>Updated:</h1>{{.Message}}</body></html>`
	if err := os.WriteFile(templatePath, []byte(updatedContent), 0600); err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}

	// Small delay to ensure file modification time changes
	time.Sleep(100 * time.Millisecond)

	// Force reload
	err = tm.ReloadTemplates()
	if err != nil {
		t.Errorf("Failed to reload templates: %v", err)
	}

	// Render updated template
	renderedUpdated, err := tm.RenderTemplate("reload.html", data)
	if err != nil {
		t.Errorf("Failed to render updated template: %v", err)
	}

	if !containsString(renderedUpdated, "Updated:") {
		t.Errorf("Updated render should contain 'Updated:', got: %s", renderedUpdated)
	}
}

func TestTemplateManager_NestedTemplates(t *testing.T) {
	// Create a temporary directory with nested template structure
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "emails")
	if err := os.MkdirAll(subDir, 0750); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	// Create nested template
	templateContent := `<html><body><p>{{.Content}}</p></body></html>`
	templatePath := filepath.Join(subDir, "nested.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write nested template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "emails/nested.html",
	}

	tm := mail.NewTemplateManager(config).WithFileServer(tempDir)

	if tm.Error != nil {
		t.Fatalf("Unexpected error: %v", tm.Error)
	}

	// Load nested template
	template, err := tm.LoadTemplate("emails/nested.html")
	if err != nil {
		t.Errorf("Failed to load nested template: %v", err)
	}

	if template == nil {
		t.Error("Expected non-nil template")
	}

	// Render nested template
	data := map[string]string{"Content": "Nested content"}
	rendered, err := tm.RenderTemplate("emails/nested.html", data)
	if err != nil {
		t.Errorf("Failed to render nested template: %v", err)
	}

	if !containsString(rendered, "Nested content") {
		t.Errorf("Rendered template should contain nested content, got: %s", rendered)
	}
}

func TestTemplateManager_ErrorHandling(t *testing.T) {
	// Test template with syntax error
	tempDir := t.TempDir()

	// Create template with syntax error
	badTemplate := `<html><body>{{.InvalidSyntax</body></html>` // Missing closing }}
	templatePath := filepath.Join(tempDir, "bad.html")
	if err := os.WriteFile(templatePath, []byte(badTemplate), 0600); err != nil {
		t.Fatalf("Failed to write bad template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "bad.html",
	}

	tm := mail.NewTemplateManager(config).WithFileServer(tempDir)

	// Should handle template parsing error gracefully
	_, err := tm.LoadTemplate("bad.html")
	if err == nil {
		t.Error("Expected error when loading template with syntax error")
	}
}

func TestTemplateManager_RenderWithComplexData(t *testing.T) {
	tempDir := t.TempDir()

	// Create template that uses complex data structures
	templateContent := `
<html>
<body>
	<h1>{{.User.Name}}</h1>
	<p>Email: {{.User.Email}}</p>
	<ul>
	{{range .Items}}
		<li>{{.}}</li>
	{{end}}
	</ul>
	<p>Count: {{len .Items}}</p>
</body>
</html>`

	templatePath := filepath.Join(tempDir, "complex.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write complex template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "complex.html",
	}

	tm := mail.NewTemplateManager(config).WithFileServer(tempDir)

	if tm.Error != nil {
		t.Fatalf("Unexpected error: %v", tm.Error)
	}

	// Complex data structure
	data := map[string]interface{}{
		"User": map[string]string{
			"Name":  "John Doe",
			"Email": "john@example.com",
		},
		"Items": []string{"Item 1", "Item 2", "Item 3"},
	}

	rendered, err := tm.RenderTemplate("complex.html", data)
	if err != nil {
		t.Errorf("Failed to render complex template: %v", err)
	}

	// Verify complex data was rendered correctly
	if !containsString(rendered, "John Doe") {
		t.Errorf("Should contain user name, got: %s", rendered)
	}

	if !containsString(rendered, "john@example.com") {
		t.Errorf("Should contain user email, got: %s", rendered)
	}

	if !containsString(rendered, "Item 1") {
		t.Errorf("Should contain first item, got: %s", rendered)
	}
}

func TestTemplateManager_DefaultTemplateFunctions(t *testing.T) {
	tempDir := t.TempDir()

	// Create template that uses built-in functions
	templateContent := `<p>Sum: {{add 5 3}}</p><p>Upper: {{upper "hello"}}</p>`

	templatePath := filepath.Join(tempDir, "functions.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write functions template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "functions.html",
	}

	// Create template manager and set up functions first
	tm := mail.NewTemplateManager(config)
	tm = tm.WithDefaultFuncs()      // Add functions first
	tm = tm.WithFileServer(tempDir) // Then load templates

	if tm.Error != nil {
		t.Fatalf("Unexpected error: %v", tm.Error)
	}

	data := map[string]interface{}{}

	rendered, err := tm.RenderTemplate("functions.html", data)
	if err != nil {
		t.Errorf("Failed to render template with functions: %v", err)
		return
	}

	// Verify functions worked (basic checks)
	if !containsString(rendered, "Sum: 8") {
		t.Errorf("Add function should work, got: %s", rendered)
	}

	if !containsString(rendered, "HELLO") {
		t.Errorf("Upper function should work, got: %s", rendered)
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTemplateManager_ChainedConfiguration(t *testing.T) {
	tempDir := t.TempDir()

	// Create a template
	templateContent := `<p>{{.Message}} - {{uppercase .Name}}</p>`
	templatePath := filepath.Join(tempDir, "chain.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "chain.html",
	}

	// Test chained configuration - add custom function first, then default funcs
	tm := mail.NewTemplateManager(config).
		WithTemplateFunc("uppercase", func(s string) string {
			// Simple uppercase implementation
			return s + "_UPPER"
		}).
		WithFileServer(tempDir).
		WithDefaultFuncs()

	if tm.Error != nil {
		t.Errorf("Chained configuration should not produce error: %v", tm.Error)
	}

	data := map[string]string{
		"Message": "Hello",
		"Name":    "world",
	}

	rendered, err := tm.RenderTemplate("chain.html", data)
	if err != nil {
		t.Errorf("Failed to render chained template: %v", err)
	}

	if !containsString(rendered, "Hello") {
		t.Errorf("Should contain message, got: %s", rendered)
	}

	if !containsString(rendered, "world_UPPER") {
		t.Errorf("Should contain uppercase name, got: %s", rendered)
	}
}
