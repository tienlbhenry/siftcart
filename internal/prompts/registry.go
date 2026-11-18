package prompts

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"strings"
	"text/template"

	"github.com/tienlbhenry/siftcart/internal/domain"
	embeddedprompts "github.com/tienlbhenry/siftcart/prompts"
	"gopkg.in/yaml.v3"
)

var (
	ErrPromptNotFound        = errors.New("prompt not found")
	ErrPromptVersionNotFound = errors.New("prompt version not found")
	ErrInvalidPromptTemplate = errors.New("invalid prompt template")
)

type PromptTemplate struct {
	Name           string           `yaml:"-"`
	Version        string           `yaml:"version"`
	Description    string           `yaml:"description"`
	ModelTier      domain.ModelTier `yaml:"model_tier"`
	Temperature    float64          `yaml:"temperature"`
	SystemTemplate string           `yaml:"system_template"`
	UserTemplate   string           `yaml:"user_template"`

	systemTmpl *template.Template
	userTmpl   *template.Template
}

type RenderedPrompt struct {
	Name        string
	Version     string
	ModelTier   domain.ModelTier
	Temperature float64
	System      string
	User        string
}

type Registry struct {
	templates map[string]map[string]*PromptTemplate
}

// NewRegistry loads all embedded prompt templates from prompts.FS.
func NewRegistry() (*Registry, error) {
	return NewRegistryFromFS(embeddedprompts.FS)
}

// NewRegistryFromFS loads and validates prompt templates organized as <name>/<version>.yaml.
func NewRegistryFromFS(fsys fs.FS) (*Registry, error) {
	if fsys == nil {
		return nil, fmt.Errorf("%w: filesystem is nil", ErrInvalidPromptTemplate)
	}

	matches, err := fs.Glob(fsys, "*/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("glob prompt templates: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: no prompt templates found", ErrInvalidPromptTemplate)
	}

	templates := make(map[string]map[string]*PromptTemplate)
	for _, filePath := range matches {
		name := path.Dir(filePath)
		raw, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return nil, fmt.Errorf("read prompt %s: %w", filePath, err)
		}

		tmpl, err := parsePromptTemplate(name, filePath, raw)
		if err != nil {
			return nil, err
		}

		if templates[name] == nil {
			templates[name] = make(map[string]*PromptTemplate)
		}
		if _, exists := templates[name][tmpl.Version]; exists {
			return nil, fmt.Errorf("%w: duplicate prompt %s version %s", ErrInvalidPromptTemplate, name, tmpl.Version)
		}
		templates[name][tmpl.Version] = tmpl
	}

	return &Registry{templates: templates}, nil
}

func parsePromptTemplate(name, filePath string, raw []byte) (*PromptTemplate, error) {
	var tmpl PromptTemplate
	if err := yaml.Unmarshal(raw, &tmpl); err != nil {
		return nil, fmt.Errorf("%w: parse yaml %s: %v", ErrInvalidPromptTemplate, filePath, err)
	}

	tmpl.Name = name
	tmpl.Version = strings.TrimSpace(tmpl.Version)
	tmpl.Description = strings.TrimSpace(tmpl.Description)
	tmpl.SystemTemplate = strings.TrimRight(tmpl.SystemTemplate, "\n")
	tmpl.UserTemplate = strings.TrimRight(tmpl.UserTemplate, "\n")

	if tmpl.Version == "" {
		return nil, fmt.Errorf("%w: %s missing version", ErrInvalidPromptTemplate, filePath)
	}
	if !tmpl.ModelTier.IsValid() {
		return nil, fmt.Errorf("%w: %s has invalid model_tier %q", ErrInvalidPromptTemplate, filePath, tmpl.ModelTier)
	}
	if math.IsNaN(tmpl.Temperature) || tmpl.Temperature < 0 || tmpl.Temperature > 2 {
		return nil, fmt.Errorf("%w: %s temperature must be between 0 and 2", ErrInvalidPromptTemplate, filePath)
	}
	if strings.TrimSpace(tmpl.SystemTemplate) == "" {
		return nil, fmt.Errorf("%w: %s missing system_template", ErrInvalidPromptTemplate, filePath)
	}
	if strings.TrimSpace(tmpl.UserTemplate) == "" {
		return nil, fmt.Errorf("%w: %s missing user_template", ErrInvalidPromptTemplate, filePath)
	}

	sysTmpl, err := template.New(filePath + ":system").Option("missingkey=error").Parse(tmpl.SystemTemplate)
	if err != nil {
		return nil, fmt.Errorf("%w: %s parse system_template: %v", ErrInvalidPromptTemplate, filePath, err)
	}
	usrTmpl, err := template.New(filePath + ":user").Option("missingkey=error").Parse(tmpl.UserTemplate)
	if err != nil {
		return nil, fmt.Errorf("%w: %s parse user_template: %v", ErrInvalidPromptTemplate, filePath, err)
	}

	tmpl.systemTmpl = sysTmpl
	tmpl.userTmpl = usrTmpl
	return &tmpl, nil
}

// Get returns a copy of the prompt template metadata for the given name and version.
func (r *Registry) Get(name, version string) (PromptTemplate, error) {
	tmpl, err := r.lookup(name, version)
	if err != nil {
		return PromptTemplate{}, err
	}
	out := *tmpl
	out.systemTmpl = nil
	out.userTmpl = nil
	return out, nil
}

// Render executes system_template and user_template for the given prompt name and version.
func (r *Registry) Render(name, version string, data any) (RenderedPrompt, error) {
	tmpl, err := r.lookup(name, version)
	if err != nil {
		return RenderedPrompt{}, err
	}

	var sysBuf bytes.Buffer
	if err := tmpl.systemTmpl.Execute(&sysBuf, data); err != nil {
		return RenderedPrompt{}, fmt.Errorf("render system_template for %s@%s: %w", name, version, err)
	}

	var usrBuf bytes.Buffer
	if err := tmpl.userTmpl.Execute(&usrBuf, data); err != nil {
		return RenderedPrompt{}, fmt.Errorf("render user_template for %s@%s: %w", name, version, err)
	}

	return RenderedPrompt{
		Name:        tmpl.Name,
		Version:     tmpl.Version,
		ModelTier:   tmpl.ModelTier,
		Temperature: tmpl.Temperature,
		System:      sysBuf.String(),
		User:        usrBuf.String(),
	}, nil
}

func (r *Registry) lookup(name, version string) (*PromptTemplate, error) {
	if r == nil {
		return nil, ErrPromptNotFound
	}
	versions, ok := r.templates[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPromptNotFound, name)
	}
	tmpl, ok := versions[version]
	if !ok {
		return nil, fmt.Errorf("%w: %s@%s", ErrPromptVersionNotFound, name, version)
	}
	return tmpl, nil
}
