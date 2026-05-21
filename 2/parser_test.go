package dockerimageparser

import (
	"bytes"
	"os"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ---- общие helpers ----

func mustVersion(s string) *semver.Version {
	v, err := semver.NewVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

func mustRef(domain, name, version string) *ImageRef {
	return &ImageRef{Domain: domain, Name: name, Version: mustVersion(version)}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/fixtures/" + name)
	require.NoError(t, err, "read fixture %s", name)
	return data
}

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ---- DockerfileStrategySuite ----

type DockerfileStrategySuite struct {
	suite.Suite
	s dockerfileStrategy
}

func (suite *DockerfileStrategySuite) SetupTest() {
	suite.s = dockerfileStrategy{}
}

func (suite *DockerfileStrategySuite) TestMatch() {
	cases := map[string]bool{
		"Dockerfile":         true,
		"Dockerfile.dev":     true,
		"Containerfile":      true,
		"containerfile.prod": true,
		"pom.xml":            false,
		"deploy.yaml":        false,
		"app.env":            false,
	}
	for path, want := range cases {
		suite.Equal(want, suite.s.Match(path), "Match(%q)", path)
	}
}

func (suite *DockerfileStrategySuite) TestParse() {
	content := readFixture(suite.T(), "Dockerfile")
	refs, err := suite.s.Parse(content)
	require.NoError(suite.T(), err)

	// alpine:latest и nginx:1.2.3 (без домена) — пропускаются
	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "docker.io", "library/nginx", "1.25.3")
	suite.Equal(testDigest, refs[0].Digest, "digest должен распарситься")
	suite.assertRef(refs[1], "ghcr.io", "myorg/myapp", "2.1.0")
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_AllMatching() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0\nFROM ghcr.io/myorg/myapp:2.1.3\nRUN echo hello\n")
	updated, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.1.5"))
	require.NoError(suite.T(), err)
	suite.Equal(
		"FROM ghcr.io/myorg/myapp:2.1.5\nFROM ghcr.io/myorg/myapp:2.1.5\nRUN echo hello\n",
		string(updated),
	)
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_PreservesAlias() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0 AS builder\nRUN make\n")
	updated, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.1.5"))
	require.NoError(suite.T(), err)
	suite.Contains(string(updated), "AS builder", "AS alias должен сохраниться")
	suite.Contains(string(updated), "2.1.5")
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_FromFixture_RoundTrip() {
	content := readFixture(suite.T(), "Dockerfile")
	updated, err := suite.s.UpdateVersion(content, mustRef("docker.io", "library/nginx", "1.26.0"))
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "docker.io", "library/nginx", "1.26.0") // обновлён
	suite.assertRef(refs[1], "ghcr.io", "myorg/myapp", "2.1.0")      // не тронут
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_PatchBump() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.3\n")
	updated, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.1.5"))
	require.NoError(suite.T(), err)
	suite.Contains(string(updated), "2.1.5")
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_LowerMinor_Identical() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0\n")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.0.9"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_LowerPatch_Identical() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.5\n")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.1.3"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *DockerfileStrategySuite) TestUpdateVersion_MajorMismatch_Identical() {
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0\n")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "3.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *DockerfileStrategySuite) assertRef(ref *ImageRef, domain, name, version string) {
	suite.T().Helper()
	suite.Equal(domain, ref.Domain, "domain")
	suite.Equal(name, ref.Name, "name")
	suite.Equal(version, ref.Version.Original(), "version")
}

// ---- GenericStrategyXmlSuite (pom.xml через generic) ----

type GenericStrategyXmlSuite struct {
	suite.Suite
	s genericStrategy
}

func (suite *GenericStrategyXmlSuite) SetupTest() {
	cc, err := compileConfig(DefaultConfig())
	require.NoError(suite.T(), err)
	suite.s = genericStrategy{extensions: cc.cfg.GenericExtensions, re: cc.genericRe}
}

func (suite *GenericStrategyXmlSuite) TestMatch() {
	cases := map[string]bool{
		"pom.xml":     true, // xml в GenericExtensions
		"build.xml":   true,
		"Dockerfile":  false,
		"deploy.yaml": true, // yaml тоже в generic
	}
	for path, want := range cases {
		suite.Equal(want, suite.s.Match(path), "Match(%q)", path)
	}
}

func (suite *GenericStrategyXmlSuite) TestParse_Inline() {
	content := readFixture(suite.T(), "pom-inline.xml")
	refs, err := suite.s.Parse(content)
	require.NoError(suite.T(), err)

	require.Len(suite.T(), refs, 4)
	suite.assertRef(refs[0], "gcr.io", "distroless/java17", "1.0.0")
	suite.assertRef(refs[1], "myregistry.io", "myapp", "2.3.1")
	suite.assertRef(refs[2], "myregistry.io", "service-b", "1.4.0")
	suite.assertRef(refs[3], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9")
}

func (suite *GenericStrategyXmlSuite) TestParse_Multiline() {
	content := readFixture(suite.T(), "pom-multiline.xml")
	refs, err := suite.s.Parse(content)
	require.NoError(suite.T(), err)

	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "gcr.io", "distroless/java17", "1.0.0")
	suite.assertRef(refs[1], "myregistry.io", "myapp", "2.3.1")
	suite.assertRef(refs[2], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9")
}

func (suite *GenericStrategyXmlSuite) TestParse_WithDigest() {
	content := readFixture(suite.T(), "pom-with-digest.xml")
	refs, err := suite.s.Parse(content)
	require.NoError(suite.T(), err)

	require.Len(suite.T(), refs, 2)
	suite.Equal(testDigest, refs[0].Digest, "digest должен распарситься")
	suite.Empty(refs[1].Digest, "у второго образа digest отсутствует")
}

func (suite *GenericStrategyXmlSuite) TestParse_NoValidImages() {
	content := readFixture(suite.T(), "pom-no-valid-images.xml")
	refs, err := suite.s.Parse(content)
	require.NoError(suite.T(), err)
	suite.Empty(refs, "все образы в фикстуре невалидны")
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_Inline_RoundTrip() {
	content := readFixture(suite.T(), "pom-inline.xml")
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")

	updated, err := suite.s.UpdateVersion(content, newRef)
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 4)
	suite.assertRef(refs[0], "gcr.io", "distroless/java17", "1.0.5")             // обновлён
	suite.assertRef(refs[1], "myregistry.io", "myapp", "2.3.1")                  // не тронут
	suite.assertRef(refs[2], "myregistry.io", "service-b", "1.4.0")              // не тронут
	suite.assertRef(refs[3], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9") // не тронут
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_Multiline_RoundTrip() {
	content := readFixture(suite.T(), "pom-multiline.xml")
	updated, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "1.0.5"))
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "gcr.io", "distroless/java17", "1.0.5") // обновлён
	suite.assertRef(refs[1], "myregistry.io", "myapp", "2.3.1")
	suite.assertRef(refs[2], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9")
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_Multiline_PreservesWhitespace() {
	content := readFixture(suite.T(), "pom-multiline.xml")
	updated, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "1.0.5"))
	require.NoError(suite.T(), err)
	suite.True(
		bytes.Contains(updated, []byte("\n              gcr.io/distroless/java17:1.0.5\n")),
		"пробелы вокруг образа должны сохраниться",
	)
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_WithDigest_ClearsDigest() {
	content := readFixture(suite.T(), "pom-with-digest.xml")
	updated, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "1.0.5"))
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	suite.assertRef(refs[0], "gcr.io", "distroless/java17", "1.0.5")
	suite.Empty(refs[0].Digest, "digest должен сброситься, т.к. newRef его не содержит")
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_PatchBump() {
	content := readFixture(suite.T(), "pom-inline.xml") // java17:1.0.0
	updated, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "1.0.3"))
	require.NoError(suite.T(), err)
	suite.Contains(string(updated), "java17:1.0.3")
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_MajorMismatch_Identical() {
	content := readFixture(suite.T(), "pom-inline.xml")
	_, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "2.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_LowerMinor_Identical() {
	content := readFixture(suite.T(), "pom-inline.xml")
	_, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "0.9.9"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *GenericStrategyXmlSuite) TestUpdateVersion_SameVersion_Identical() {
	content := readFixture(suite.T(), "pom-inline.xml") // java17:1.0.0
	_, err := suite.s.UpdateVersion(content, mustRef("gcr.io", "distroless/java17", "1.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *GenericStrategyXmlSuite) assertRef(ref *ImageRef, domain, name, version string) {
	suite.T().Helper()
	suite.Equal(domain, ref.Domain, "domain")
	suite.Equal(name, ref.Name, "name")
	suite.Equal(version, ref.Version.Original(), "version")
}

// ---- GenericStrategyYamlEnvSuite ----

type GenericStrategyYamlEnvSuite struct {
	suite.Suite
	s genericStrategy
}

func (suite *GenericStrategyYamlEnvSuite) SetupTest() {
	cc, err := compileConfig(DefaultConfig())
	require.NoError(suite.T(), err)
	suite.s = genericStrategy{extensions: cc.cfg.GenericExtensions, re: cc.genericRe}
}

func (suite *GenericStrategyYamlEnvSuite) TestMatch() {
	cases := map[string]bool{
		"docker-compose.yml": true,
		"k8s-deploy.yaml":    true,
		"config.env":         true,
		"notes.txt":          true,
		"settings.json":      true,
		"pom.xml":            true, // xml теперь в generic
		"Dockerfile":         false,
	}
	for path, want := range cases {
		suite.Equal(want, suite.s.Match(path), "Match(%q)", path)
	}
}

func (suite *GenericStrategyYamlEnvSuite) TestParse_YAML() {
	refs, err := suite.s.Parse(readFixture(suite.T(), "docker-compose.yml"))
	require.NoError(suite.T(), err)

	// redis:latest и postgres:15.4 (нет домена) — пропускаются
	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "docker.io", "library/nginx", "1.25.3")
	suite.assertRef(refs[1], "ghcr.io", "myorg/myapp", "2.1.0")
	suite.assertRef(refs[2], "ghcr.io", "myorg/worker", "1.0.4")
}

func (suite *GenericStrategyYamlEnvSuite) TestParse_Env() {
	refs, err := suite.s.Parse(readFixture(suite.T(), "images.env"))
	require.NoError(suite.T(), err)

	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "docker.io", "library/debian", "12.1.0")
	suite.Equal(testDigest, refs[0].Digest)
	suite.assertRef(refs[1], "ghcr.io", "org/tool", "3.0.1")
}

func (suite *GenericStrategyYamlEnvSuite) TestUpdateVersion_YAML_RoundTrip() {
	content := readFixture(suite.T(), "docker-compose.yml")
	updated, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.2.0"))
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "docker.io", "library/nginx", "1.25.3") // не тронут
	suite.assertRef(refs[1], "ghcr.io", "myorg/myapp", "2.2.0")      // обновлён
	suite.assertRef(refs[2], "ghcr.io", "myorg/worker", "1.0.4")     // не тронут
}

func (suite *GenericStrategyYamlEnvSuite) TestUpdateVersion_Env_RoundTrip() {
	content := readFixture(suite.T(), "images.env")
	updated, err := suite.s.UpdateVersion(content, mustRef("docker.io", "library/debian", "12.2.0"))
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "docker.io", "library/debian", "12.2.0") // обновлён
	suite.assertRef(refs[1], "ghcr.io", "org/tool", "3.0.1")          // не тронут
}

func (suite *GenericStrategyYamlEnvSuite) TestUpdateVersion_PatchBump() {
	content := readFixture(suite.T(), "docker-compose.yml") // nginx:1.25.3
	updated, err := suite.s.UpdateVersion(content, mustRef("docker.io", "library/nginx", "1.25.9"))
	require.NoError(suite.T(), err)
	suite.Contains(string(updated), "nginx:1.25.9")
	suite.Contains(string(updated), "myorg/myapp:2.1.0", "не относящийся образ не тронут")
}

func (suite *GenericStrategyYamlEnvSuite) TestUpdateVersion_MajorMismatch_Identical() {
	content := readFixture(suite.T(), "docker-compose.yml")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "3.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *GenericStrategyYamlEnvSuite) assertRef(ref *ImageRef, domain, name, version string) {
	suite.T().Helper()
	suite.Equal(domain, ref.Domain, "domain")
	suite.Equal(name, ref.Name, "name")
	suite.Equal(version, ref.Version.Original(), "version")
}

// ---- ShouldUpdateSuite ----

type ShouldUpdateSuite struct {
	suite.Suite
}

func (suite *ShouldUpdateSuite) TestCases() {
	cases := []struct {
		old, new *ImageRef
		want     bool
		reason   string
	}{
		{mustRef("g", "a/b", "1.2.0"), mustRef("g", "a/b", "1.3.0"), true, "minor выше"},
		{mustRef("g", "a/b", "1.2.0"), mustRef("g", "a/b", "1.2.1"), true, "patch выше"},
		{mustRef("g", "a/b", "1.2.0"), mustRef("g", "a/b", "2.0.0"), false, "major отличается"},
		{mustRef("g", "a/b", "1.2.0"), mustRef("g", "a/b", "1.1.9"), false, "minor ниже"},
		{mustRef("g", "a/b", "1.2.0"), mustRef("g", "a/b", "1.2.0"), false, "равная версия"},
		{mustRef("g", "a/b", "1.2.3"), mustRef("x", "a/b", "1.2.4"), false, "domain отличается"},
		{mustRef("g", "a/b", "1.2.3"), mustRef("g", "x/y", "1.2.4"), false, "name отличается"},
	}
	for _, c := range cases {
		suite.Equal(c.want, shouldUpdate(c.old, c.new), "[%s]", c.reason)
	}
}

// ---- ConfigSuite ----

type ConfigSuite struct {
	suite.Suite
}

func (suite *ConfigSuite) TestNewFromFile_LoadsPattern() {
	// Пишем временный конфиг с кастомным паттерном
	cfg := `
generic_extensions: [yaml, txt]
generic_pattern: '(?P<domain>myrepo\.io)/(?P<name>[a-z]+):(?P<version>[0-9.]+)(?P<digest>)'
`
	f, err := os.CreateTemp("", "cfg-*.yaml")
	require.NoError(suite.T(), err)
	defer os.Remove(f.Name())
	_, err = f.WriteString(cfg)
	require.NoError(suite.T(), err)
	f.Close()

	p, err := NewFromFile(f.Name())
	require.NoError(suite.T(), err)

	refs, err := p.Parse("notes.txt", []byte("myrepo.io/myapp:1.2.3"))
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 1)
	suite.Equal("myrepo.io", refs[0].Domain)
	suite.Equal("myapp", refs[0].Name)
	suite.Equal("1.2.3", refs[0].Version.Original())
}

func (suite *ConfigSuite) TestNewFromFile_MissingFile() {
	_, err := NewFromFile("nonexistent.yaml")
	suite.Error(err)
	suite.Contains(err.Error(), "read config")
}

func (suite *ConfigSuite) TestDefaultConfig_FallbackOnEmpty() {
	// Пустой конфиг → должны примениться дефолты
	p, err := New(Config{})
	require.NoError(suite.T(), err)
	assert.NotNil(suite.T(), p)
}

// ---- регистрация suite ----

func TestDockerfileStrategy(t *testing.T)     { suite.Run(t, new(DockerfileStrategySuite)) }
func TestGenericStrategyXml(t *testing.T)     { suite.Run(t, new(GenericStrategyXmlSuite)) }
func TestGenericStrategyYamlEnv(t *testing.T) { suite.Run(t, new(GenericStrategyYamlEnvSuite)) }
func TestShouldUpdate(t *testing.T)           { suite.Run(t, new(ShouldUpdateSuite)) }
func TestConfig(t *testing.T)                 { suite.Run(t, new(ConfigSuite)) }

// ---- KustomizeStrategySuite ----

type KustomizeStrategySuite struct {
	suite.Suite
	s kustomizeStrategy
}

func (suite *KustomizeStrategySuite) SetupTest() {
	suite.s = kustomizeStrategy{}
}

func (suite *KustomizeStrategySuite) TestMatch() {
	cases := map[string]bool{
		"kustomization.yaml":        true,
		"kustomization.yml":         true,
		"KUSTOMIZATION.YAML":        true,
		"values.yaml":               false,
		"docker-compose.yml":        false,
		"Dockerfile":                false,
	}
	for path, want := range cases {
		suite.Equal(want, suite.s.Match(path), "Match(%q)", path)
	}
}

func (suite *KustomizeStrategySuite) TestParse() {
	refs, err := suite.s.Parse(readFixture(suite.T(), "kustomization.yaml"))
	require.NoError(suite.T(), err)

	// no-tag запись пропускается → 3 образа
	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "ghcr.io", "myorg/myapp", "2.1.0")
	suite.assertRef(refs[1], "ghcr.io", "myorg/service", "1.4.0") // newName применён
	suite.assertRef(refs[2], "gcr.io", "distroless/java17", "1.0.0")
	suite.Equal(testDigest, refs[2].Digest)
}

func (suite *KustomizeStrategySuite) TestUpdateVersion_NewTag_RoundTrip() {
	content := readFixture(suite.T(), "kustomization.yaml")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.2.0")

	updated, err := suite.s.UpdateVersion(content, newRef)
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 3)
	suite.assertRef(refs[0], "ghcr.io", "myorg/myapp", "2.2.0")    // обновлён
	suite.assertRef(refs[1], "ghcr.io", "myorg/service", "1.4.0")  // не тронут
	suite.assertRef(refs[2], "gcr.io", "distroless/java17", "1.0.0") // не тронут
}

func (suite *KustomizeStrategySuite) TestUpdateVersion_PatchBump() {
	content := readFixture(suite.T(), "kustomization.yaml")
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")

	updated, err := suite.s.UpdateVersion(content, newRef)
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	suite.assertRef(refs[2], "gcr.io", "distroless/java17", "1.0.5")
}

func (suite *KustomizeStrategySuite) TestUpdateVersion_MajorMismatch_Identical() {
	content := readFixture(suite.T(), "kustomization.yaml")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "3.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *KustomizeStrategySuite) TestUpdateVersion_LowerMinor_Identical() {
	content := readFixture(suite.T(), "kustomization.yaml")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.0.9"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *KustomizeStrategySuite) assertRef(ref *ImageRef, domain, name, version string) {
	suite.T().Helper()
	suite.Equal(domain, ref.Domain)
	suite.Equal(name, ref.Name)
	suite.Equal(version, ref.Version.Original())
}

// ---- HelmStrategySuite ----

type HelmStrategySuite struct {
	suite.Suite
	s helmStrategy
}

func (suite *HelmStrategySuite) SetupTest() {
	suite.s = helmStrategy{}
}

func (suite *HelmStrategySuite) TestMatch() {
	cases := map[string]bool{
		"values.yaml":               true,
		"values.yml":                true,
		"values-prod.yaml":          true,
		"values-staging.yml":        true,
		"kustomization.yaml":        false,
		"docker-compose.yml":        false,
		"Dockerfile":                false,
		"deployment.yaml":           false,
	}
	for path, want := range cases {
		suite.Equal(want, suite.s.Match(path), "Match(%q)", path)
	}
}

func (suite *HelmStrategySuite) TestParse() {
	refs, err := suite.s.Parse(readFixture(suite.T(), "values.yaml"))
	require.NoError(suite.T(), err)

	// busybox (нет домена) и latest (нон-semver) — пропускаются
	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "ghcr.io", "myorg/myapp", "2.1.0")
	suite.assertRef(refs[1], "ghcr.io", "myorg/sidecar", "1.0.3")
}

func (suite *HelmStrategySuite) TestParse_ValuesProd() {
	refs, err := suite.s.Parse(readFixture(suite.T(), "values-prod.yaml"))
	require.NoError(suite.T(), err)

	require.Len(suite.T(), refs, 1)
	suite.assertRef(refs[0], "ghcr.io", "myorg/myapp", "2.0.5")
}

func (suite *HelmStrategySuite) TestUpdateVersion_RoundTrip() {
	content := readFixture(suite.T(), "values.yaml")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.2.0")

	updated, err := suite.s.UpdateVersion(content, newRef)
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	require.Len(suite.T(), refs, 2)
	suite.assertRef(refs[0], "ghcr.io", "myorg/myapp", "2.2.0")   // обновлён
	suite.assertRef(refs[1], "ghcr.io", "myorg/sidecar", "1.0.3") // не тронут
}

func (suite *HelmStrategySuite) TestUpdateVersion_PatchBump() {
	content := readFixture(suite.T(), "values.yaml")
	newRef := mustRef("ghcr.io", "myorg/sidecar", "1.0.9")

	updated, err := suite.s.UpdateVersion(content, newRef)
	require.NoError(suite.T(), err)

	refs, err := suite.s.Parse(updated)
	require.NoError(suite.T(), err)
	suite.assertRef(refs[1], "ghcr.io", "myorg/sidecar", "1.0.9")
}

func (suite *HelmStrategySuite) TestUpdateVersion_MajorMismatch_Identical() {
	content := readFixture(suite.T(), "values.yaml")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "3.0.0"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *HelmStrategySuite) TestUpdateVersion_LowerMinor_Identical() {
	content := readFixture(suite.T(), "values.yaml")
	_, err := suite.s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.0.9"))
	suite.ErrorIs(err, ErrContentIdentical)
}

func (suite *HelmStrategySuite) assertRef(ref *ImageRef, domain, name, version string) {
	suite.T().Helper()
	suite.Equal(domain, ref.Domain)
	suite.Equal(name, ref.Name)
	suite.Equal(version, ref.Version.Original())
}

func TestKustomizeStrategy(t *testing.T) { suite.Run(t, new(KustomizeStrategySuite)) }
func TestHelmStrategy(t *testing.T)      { suite.Run(t, new(HelmStrategySuite)) }
