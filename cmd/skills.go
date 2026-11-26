package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"
)

// The server publishes the speak skill through the MCP Skills extension
// (SEP-2640), so a client that supports it loads the skill from the server and
// the skill always matches the server's version.
const (
	skillsExtension  = "io.modelcontextprotocol/skills"
	methodSkillsList = "skills/list"
	methodSkillsGet  = "skills/get"
	// skillDir is the directory of the embedded files that holds the skill.
	skillDir = "skill"
)

// skillsCache lets clients reuse a listing for five minutes. The skill is fixed
// for the life of the process, so a stale entry is caught by its digests.
var skillsCache = mcp.Cacheable{TTLMs: 300_000, CacheScope: "public"}

// skillResource is one file in a skill entry's manifest.
type skillResource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

// skillEntry is the extension's Skill entry for one skill.
type skillEntry struct {
	URI         string          `json:"uri"`
	Frontmatter map[string]any  `json:"frontmatter"`
	Resources   []skillResource `json:"resources"`
}

type listSkillsParams struct {
	mcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type listSkillsResult struct {
	mcp.ResultBase
	mcp.Cacheable
	ResultType string       `json:"resultType"`
	Skills     []skillEntry `json:"skills"`
}

type getSkillParams struct {
	mcp.ParamsBase
	URI string `json:"uri"`
}

type getSkillResult struct {
	mcp.ResultBase
	mcp.Cacheable
	ResultType string     `json:"resultType"`
	Skill      skillEntry `json:"skill"`
}

// addSkill publishes the skill in the skillDir directory of files: SKILL.md
// with its frontmatter at the root, plus supporting files. Each file becomes a
// skill:// resource, and skills/list and skills/get return the skill's entry.
// The caller declares the extension; the SDK declares resources once they exist.
func addSkill(server *mcp.Server, files fs.FS) error {
	root, err := fs.Sub(files, skillDir)
	if err != nil {
		return fmt.Errorf("opening the %s directory: %w", skillDir, err)
	}
	skillMD, err := fs.ReadFile(root, "SKILL.md")
	if err != nil {
		return fmt.Errorf("reading the skill: %w", err)
	}
	frontmatter, err := parseFrontmatter(skillMD)
	if err != nil {
		return err
	}
	name, _ := frontmatter["name"].(string)
	description, _ := frontmatter["description"].(string)
	entry := skillEntry{URI: "skill://" + name + "/SKILL.md", Frontmatter: frontmatter}

	err = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		uri := "skill://" + name + "/" + p
		sum := sha256.Sum256(data)
		entry.Resources = append(entry.Resources, skillResource{
			URI: uri, Digest: "sha256:" + hex.EncodeToString(sum[:]), Size: len(data),
		})
		resource := &mcp.Resource{URI: uri, Name: path.Base(p), MIMEType: skillMIMEType(p), Size: int64(len(data))}
		if p == "SKILL.md" {
			resource.Name, resource.Description = name, description
		}
		contents := &mcp.ResourceContents{URI: uri, MIMEType: resource.MIMEType, Text: string(data)}
		server.AddResource(resource, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading the skill files: %w", err)
	}
	return addSkillMethods(server, entry)
}

// addSkillMethods answers skills/list and skills/get with entry, the one skill
// this server serves.
func addSkillMethods(server *mcp.Server, entry skillEntry) error {
	err := mcp.AddReceivingCustomMethod(server, methodSkillsList,
		func(context.Context, *mcp.ServerSession, *listSkillsParams) (*listSkillsResult, error) {
			return &listSkillsResult{Cacheable: skillsCache, ResultType: "complete", Skills: []skillEntry{entry}}, nil
		})
	if err != nil {
		return err
	}
	return mcp.AddReceivingCustomMethod(server, methodSkillsGet,
		func(_ context.Context, _ *mcp.ServerSession, params *getSkillParams) (*getSkillResult, error) {
			if params == nil || params.URI != entry.URI {
				var uri string
				if params != nil {
					uri = params.URI
				}
				return nil, &jsonrpc.Error{
					Code:    jsonrpc.CodeInvalidParams,
					Message: fmt.Sprintf("No skill is served at %q; this server serves %s", uri, entry.URI),
				}
			}
			return &getSkillResult{Cacheable: skillsCache, ResultType: "complete", Skill: entry}, nil
		})
}

// parseFrontmatter returns the YAML frontmatter at the start of a SKILL.md. A
// Windows checkout can carry CRLF line endings, so those are read as LF.
func parseFrontmatter(skillMD []byte) (map[string]any, error) {
	text := bytes.ReplaceAll(skillMD, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(text, []byte("---\n"))
	if !ok {
		return nil, errors.New("SKILL.md must start with YAML frontmatter between --- lines")
	}
	header, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, errors.New("SKILL.md frontmatter has no closing --- line")
	}
	var frontmatter map[string]any
	if err := yaml.Unmarshal(header, &frontmatter); err != nil {
		return nil, fmt.Errorf("parsing SKILL.md frontmatter: %w", err)
	}
	return frontmatter, nil
}

func skillMIMEType(name string) string {
	switch path.Ext(name) {
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "text/plain"
	}
}
