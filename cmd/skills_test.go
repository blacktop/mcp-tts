package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSkillMD = "---\nname: demo\ndescription: \"Says hello\"\nlicense: MIT\n---\n\n# Demo\n"

func testSkillFS() fstest.MapFS {
	return fstest.MapFS{
		"skill/SKILL.md":             {Data: []byte(testSkillMD)},
		"skill/references/data.json": {Data: []byte(`{"a":1}`)},
	}
}

func sha(data string) string {
	sum := sha256.Sum256([]byte(data))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// skillSession publishes the skill in files and returns a client session that
// can call the skills methods, as a host would.
func skillSession(t *testing.T, files fs.FS) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "1"}, nil)
	require.NoError(t, addSkill(server, files))
	client := mcp.NewClient(&mcp.Implementation{Name: "skills-test-client", Version: "1"}, nil)
	require.NoError(t, mcp.AddSendingCustomMethod[*listSkillsParams, *listSkillsResult](client, methodSkillsList))
	require.NoError(t, mcp.AddSendingCustomMethod[*getSkillParams, *getSkillResult](client, methodSkillsGet))

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func listSkills(t *testing.T, session *mcp.ClientSession) *listSkillsResult {
	t.Helper()
	result, err := mcp.CallCustomMethod[*listSkillsParams, *listSkillsResult](
		context.Background(), session, methodSkillsList, &listSkillsParams{})
	require.NoError(t, err)
	return result
}

func getSkill(session *mcp.ClientSession, uri string) (*getSkillResult, error) {
	return mcp.CallCustomMethod[*getSkillParams, *getSkillResult](
		context.Background(), session, methodSkillsGet, &getSkillParams{URI: uri})
}

func TestSkillsListAndGetReturnTheEntry(t *testing.T) {
	session := skillSession(t, testSkillFS())
	want := skillEntry{
		URI:         "skill://demo/SKILL.md",
		Frontmatter: map[string]any{"name": "demo", "description": "Says hello", "license": "MIT"},
		Resources: []skillResource{
			{URI: "skill://demo/SKILL.md", Digest: sha(testSkillMD), Size: len(testSkillMD)},
			{URI: "skill://demo/references/data.json", Digest: sha(`{"a":1}`), Size: 7},
		},
	}

	list := listSkills(t, session)
	assert.Equal(t, []skillEntry{want}, list.Skills)
	assert.Equal(t, "complete", list.ResultType)
	assert.Equal(t, skillsCache, list.Cacheable, "list and get results must carry cache hints")

	got, err := getSkill(session, want.URI)
	require.NoError(t, err)
	assert.Equal(t, want, got.Skill)
	assert.Equal(t, "complete", got.ResultType)
	assert.Equal(t, skillsCache, got.Cacheable)
}

func TestSkillsGetRejectsAnUnknownSkill(t *testing.T) {
	session := skillSession(t, testSkillFS())
	for name, uri := range map[string]string{"unknown skill": "skill://other/SKILL.md", "no uri": ""} {
		t.Run(name, func(t *testing.T) {
			_, err := getSkill(session, uri)
			var wire *jsonrpc.Error
			require.ErrorAs(t, err, &wire)
			assert.Equal(t, int64(jsonrpc.CodeInvalidParams), wire.Code)
		})
	}
}

func TestSkillResourcesServeTheFiles(t *testing.T) {
	session := skillSession(t, testSkillFS())

	listed, err := session.ListResources(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, listed.Resources, 2)
	byURI := map[string]*mcp.Resource{}
	for _, r := range listed.Resources {
		byURI[r.URI] = r
	}
	skillMD := byURI["skill://demo/SKILL.md"]
	require.NotNil(t, skillMD)
	assert.Equal(t, "demo", skillMD.Name)
	assert.Equal(t, "Says hello", skillMD.Description)
	assert.Equal(t, "text/markdown", skillMD.MIMEType)
	assert.Equal(t, "application/json", byURI["skill://demo/references/data.json"].MIMEType)

	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "skill://demo/references/data.json"})
	require.NoError(t, err)
	require.Len(t, read.Contents, 1)
	assert.Equal(t, `{"a":1}`, read.Contents[0].Text)
}

// The real skill must keep loading, so a bad edit to skill/ fails here rather
// than at server startup.
func TestAddSkillPublishesTheBundledSpeakSkill(t *testing.T) {
	entry := listSkills(t, skillSession(t, os.DirFS(".."))).Skills[0]
	assert.Equal(t, "skill://speak/SKILL.md", entry.URI)
	assert.Equal(t, "speak", entry.Frontmatter["name"])
	assert.NotEmpty(t, entry.Frontmatter["description"])
}

// Git on Windows checks files out with CRLF line endings by default.
func TestAddSkillAcceptsCRLFLineEndings(t *testing.T) {
	crlf := strings.ReplaceAll(testSkillMD, "\n", "\r\n")
	entry := listSkills(t, skillSession(t, fstest.MapFS{"skill/SKILL.md": {Data: []byte(crlf)}})).Skills[0]
	assert.Equal(t, map[string]any{"name": "demo", "description": "Says hello", "license": "MIT"}, entry.Frontmatter)
	assert.Equal(t, sha(crlf), entry.Resources[0].Digest, "the served bytes stay as checked out")
}

func TestAddSkillRejectsInvalidSkills(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{"no SKILL.md", fstest.MapFS{"skill/README.md": {Data: []byte("x")}}, "reading the skill"},
		{"no frontmatter", fstest.MapFS{"skill/SKILL.md": {Data: []byte("# Demo\n")}}, "must start with YAML frontmatter"},
		{"unclosed frontmatter", fstest.MapFS{"skill/SKILL.md": {Data: []byte("---\nname: demo\n")}}, "no closing --- line"},
		{"bad YAML", fstest.MapFS{"skill/SKILL.md": {Data: []byte("---\nname: [demo\n---\n")}}, "parsing SKILL.md frontmatter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "1"}, nil)
			require.ErrorContains(t, addSkill(server, tc.files), tc.wantErr)
		})
	}
}
