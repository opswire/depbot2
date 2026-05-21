package dockerimageparser

import (
	"bytes"
	"os"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// ---- helpers ----

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
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ---- Dockerfile ----

func TestDockerfile_Match(t *testing.T) {
	s := dockerfileStrategy{}
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
		if got := s.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDockerfile_Parse(t *testing.T) {
	s := dockerfileStrategy{}
	content := readFixture(t, "Dockerfile")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	// alpine:latest и nginx:1.2.3 — без домена или нон-semver → пропускаются
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0].Domain != "docker.io" || refs[0].Name != "library/nginx" || refs[0].Version.String() != "1.25.3" {
		t.Errorf("unexpected refs[0]: %v", refs[0])
	}
	if refs[0].Digest != testDigest {
		t.Errorf("refs[0] digest not parsed: %q", refs[0].Digest)
	}
	if refs[1].Domain != "ghcr.io" || refs[1].Name != "myorg/myapp" || refs[1].Version.String() != "2.1.0" {
		t.Errorf("unexpected refs[1]: %v", refs[1])
	}
}

func TestDockerfile_UpdateVersion_AllMatching(t *testing.T) {
	s := dockerfileStrategy{}
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0\nFROM ghcr.io/myorg/myapp:2.1.3\nRUN echo hello\n")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.1.5")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	want := "FROM ghcr.io/myorg/myapp:2.1.5\nFROM ghcr.io/myorg/myapp:2.1.5\nRUN echo hello\n"
	if string(updated) != want {
		t.Errorf("got:\n%s\nwant:\n%s", updated, want)
	}
}

func TestDockerfile_UpdateVersion_PreservesAlias(t *testing.T) {
	s := dockerfileStrategy{}
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0 AS builder\nRUN make\n")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.1.5")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	// AS alias должен сохраниться
	if !bytes.Contains(updated, []byte("AS builder")) {
		t.Errorf("alias lost: %s", updated)
	}
	if !bytes.Contains(updated, []byte("2.1.5")) {
		t.Errorf("version not updated: %s", updated)
	}
}

func TestDockerfile_UpdateVersion_Identical(t *testing.T) {
	s := dockerfileStrategy{}
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.0\n")
	// minor ниже — не обновляем
	_, err := s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.0.9"))
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical, got %v", err)
	}
}

// ---- pom.xml ----

func TestPOM_Match(t *testing.T) {
	s := pomStrategy{}
	cases := map[string]bool{
		"pom.xml":     true,
		"POM.XML":     true,
		"deploy.yaml": false,
		"Dockerfile":  false,
		"app.env":     false,
	}
	for path, want := range cases {
		if got := s.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPOM_Parse_Inline(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	// gcr.io/distroless/java17:1.0.0, myregistry.io/myapp:2.3.1,
	// myregistry.io/service-b:1.4.0, eclipse-temurin/17-jre-alpine:17.0.9
	if len(refs) != 4 {
		t.Fatalf("expected 4 refs, got %d: %v", len(refs), refs)
	}
	assertRef(t, refs[0], "gcr.io", "distroless/java17", "1.0.0")
	assertRef(t, refs[1], "myregistry.io", "myapp", "2.3.1")
	assertRef(t, refs[2], "myregistry.io", "service-b", "1.4.0")
	assertRef(t, refs[3], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9")
}

func TestPOM_Parse_Multiline(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-multiline.xml")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	// Те же образы что и inline, но с переносами строк в теге
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d: %v", len(refs), refs)
	}
	assertRef(t, refs[0], "gcr.io", "distroless/java17", "1.0.0")
	assertRef(t, refs[1], "myregistry.io", "myapp", "2.3.1")
	assertRef(t, refs[2], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9")
}

func TestPOM_Parse_WithDigest(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-with-digest.xml")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0].Digest != testDigest {
		t.Errorf("digest not parsed: %q", refs[0].Digest)
	}
	if refs[1].Digest != "" {
		t.Errorf("unexpected digest on refs[1]: %q", refs[1].Digest)
	}
}

func TestPOM_Parse_NoValidImages(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-no-valid-images.xml")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	// Все образы в фикстуре невалидны (нет домена / нон-semver / нет версии)
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d: %v", len(refs), refs)
	}
}

func TestPOM_UpdateVersion_Inline(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml")
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("gcr.io/distroless/java17:1.0.5")) {
		t.Errorf("version not updated:\n%s", updated)
	}
	// Старая версия не должна остаться
	if bytes.Contains(updated, []byte("java17:1.0.0")) {
		t.Errorf("old version still present:\n%s", updated)
	}
}

func TestPOM_UpdateVersion_Multiline_PreservesWhitespace(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-multiline.xml")
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	// Переносы строк вокруг образа должны сохраниться
	if !bytes.Contains(updated, []byte("\n              gcr.io/distroless/java17:1.0.5\n")) {
		t.Errorf("whitespace not preserved or version not updated:\n%s", updated)
	}
}

func TestPOM_UpdateVersion_MajorMismatch(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml")
	// major отличается — не обновляем
	newRef := mustRef("gcr.io", "distroless/java17", "2.0.0")

	_, err := s.UpdateVersion(content, newRef)
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical, got %v", err)
	}
}

func TestPOM_UpdateVersion_LowerMinor(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml") // java17:1.0.0
	// minor ниже — не обновляем
	newRef := mustRef("gcr.io", "distroless/java17", "0.9.9")

	_, err := s.UpdateVersion(content, newRef)
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical, got %v", err)
	}
}

// ---- Generic (yaml, env) ----

func TestGeneric_Match(t *testing.T) {
	s := defaultGenericStrategy(t)
	cases := map[string]bool{
		"docker-compose.yml": true,
		"k8s-deploy.yaml":    true,
		"config.env":         true,
		"notes.txt":          true,
		"settings.json":      true,
		"Dockerfile":         false, // dockerfile-стратегия
		"pom.xml":            false, // pom-стратегия
	}
	for path, want := range cases {
		if got := s.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestGeneric_Parse_YAML(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "docker-compose.yml")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	// redis:latest (нон-semver) и postgres:15.4 (нет домена) — пропускаются
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d: %v", len(refs), refs)
	}
	assertRef(t, refs[0], "docker.io", "library/nginx", "1.25.3")
	assertRef(t, refs[1], "ghcr.io", "myorg/myapp", "2.1.0")
	assertRef(t, refs[2], "ghcr.io", "myorg/worker", "1.0.4")
}

func TestGeneric_Parse_Env(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "images.env")

	refs, err := s.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	assertRef(t, refs[0], "docker.io", "library/debian", "12.1.0")
	if refs[0].Digest != testDigest {
		t.Errorf("digest not parsed: %q", refs[0].Digest)
	}
	assertRef(t, refs[1], "ghcr.io", "org/tool", "3.0.1")
}

func TestGeneric_UpdateVersion_YAML(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "docker-compose.yml")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.2.0")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("ghcr.io/myorg/myapp:2.2.0")) {
		t.Errorf("version not updated:\n%s", updated)
	}
	// Другие образы не тронуты
	if !bytes.Contains(updated, []byte("docker.io/library/nginx:1.25.3")) {
		t.Errorf("unrelated image was modified:\n%s", updated)
	}
}

func TestGeneric_UpdateVersion_Identical(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "docker-compose.yml")
	// major отличается — ничего не обновляем
	newRef := mustRef("ghcr.io", "myorg/myapp", "3.0.0")

	_, err := s.UpdateVersion(content, newRef)
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical, got %v", err)
	}
}

// ---- shouldUpdate ----

func TestShouldUpdate(t *testing.T) {
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
		if got := shouldUpdate(c.old, c.new); got != c.want {
			t.Errorf("[%s] shouldUpdate(%s -> %s) = %v, want %v",
				c.reason, c.old, c.new, got, c.want)
		}
	}
}

// ---- helpers для тестов ----

func assertRef(t *testing.T, ref *ImageRef, domain, name, version string) {
	t.Helper()
	if ref.Domain != domain {
		t.Errorf("domain: got %q, want %q", ref.Domain, domain)
	}
	if ref.Name != name {
		t.Errorf("name: got %q, want %q", ref.Name, name)
	}
	if ref.Version.Original() != version {
		t.Errorf("version: got %q, want %q", ref.Version.Original(), version)
	}
}

func defaultPomStrategy(t *testing.T) pomStrategy {
	t.Helper()
	cc, err := compileConfig(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return pomStrategy{re: cc.pomRe}
}

func defaultGenericStrategy(t *testing.T) genericStrategy {
	t.Helper()
	cc, err := compileConfig(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return genericStrategy{extensions: cc.cfg.GenericExtensions, re: cc.genericRe}
}

// ---- UpdateVersion: расширенные тесты ----

// Dockerfile: обновление из файла-фикстуры — проверяем точный результат через повторный Parse
func TestDockerfile_UpdateVersion_FromFixture_RoundTrip(t *testing.T) {
	s := dockerfileStrategy{}
	content := readFixture(t, "Dockerfile")

	// Обновляем nginx 1.25.3 → 1.26.0 (minor выше)
	newRef := mustRef("docker.io", "library/nginx", "1.26.0")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}

	// Перепарсим и проверим результат
	refs, err := s.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs after update, got %d", len(refs))
	}
	// nginx обновлён
	assertRef(t, refs[0], "docker.io", "library/nginx", "1.26.0")
	// myapp не тронут
	assertRef(t, refs[1], "ghcr.io", "myorg/myapp", "2.1.0")
}

// Dockerfile: patch-обновление (minor совпадает, patch выше)
func TestDockerfile_UpdateVersion_PatchBump(t *testing.T) {
	s := dockerfileStrategy{}
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.3\n")
	newRef := mustRef("ghcr.io", "myorg/myapp", "2.1.5")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("2.1.5")) {
		t.Errorf("patch not updated: %s", updated)
	}
}

// Dockerfile: patch ниже → ErrContentIdentical
func TestDockerfile_UpdateVersion_LowerPatch(t *testing.T) {
	s := dockerfileStrategy{}
	content := []byte("FROM ghcr.io/myorg/myapp:2.1.5\n")
	_, err := s.UpdateVersion(content, mustRef("ghcr.io", "myorg/myapp", "2.1.3"))
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical, got %v", err)
	}
}

// pom: round-trip — Parse → UpdateVersion → Parse, проверяем результат парсинга
func TestPOM_UpdateVersion_Inline_RoundTrip(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml")

	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}

	// Перепарсим и проверим что именно изменилось
	refs, err := s.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 4 {
		t.Fatalf("expected 4 refs after update, got %d", len(refs))
	}
	assertRef(t, refs[0], "gcr.io", "distroless/java17", "1.0.5")  // обновлён
	assertRef(t, refs[1], "myregistry.io", "myapp", "2.3.1")        // не тронут
	assertRef(t, refs[2], "myregistry.io", "service-b", "1.4.0")    // не тронут
	assertRef(t, refs[3], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9") // не тронут
}

// pom: multiline round-trip
func TestPOM_UpdateVersion_Multiline_RoundTrip(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-multiline.xml")

	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := s.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs after update, got %d", len(refs))
	}
	assertRef(t, refs[0], "gcr.io", "distroless/java17", "1.0.5") // обновлён
	assertRef(t, refs[1], "myregistry.io", "myapp", "2.3.1")       // не тронут
	assertRef(t, refs[2], "docker.io", "eclipse-temurin/17-jre-alpine", "17.0.9") // не тронут
}

// pom: образ с digest — после обновления digest сбрасывается (новый ref без digest)
func TestPOM_UpdateVersion_WithDigest_RoundTrip(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-with-digest.xml")

	// Обновляем образ с digest → новый ref без digest
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.5")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := s.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs after update, got %d", len(refs))
	}
	assertRef(t, refs[0], "gcr.io", "distroless/java17", "1.0.5")
	// digest сброшен, т.к. newRef его не содержит
	if refs[0].Digest != "" {
		t.Errorf("expected digest to be cleared, got %q", refs[0].Digest)
	}
}

// pom: patch-обновление
func TestPOM_UpdateVersion_PatchBump(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml") // java17:1.0.0
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.3")

	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("java17:1.0.3")) {
		t.Errorf("patch not updated:\n%s", updated)
	}
}

// pom: patch ниже → ErrContentIdentical
func TestPOM_UpdateVersion_LowerPatch(t *testing.T) {
	s := defaultPomStrategy(t)
	content := readFixture(t, "pom-inline.xml") // java17:1.0.0
	// patch ниже (0 > -1, но -1 не валиден; берём реальный случай: 1.0.0 → 1.0.0 patch совпадает)
	newRef := mustRef("gcr.io", "distroless/java17", "1.0.0")
	_, err := s.UpdateVersion(content, newRef)
	if err != ErrContentIdentical {
		t.Errorf("expected ErrContentIdentical for same version, got %v", err)
	}
}

// generic: round-trip на .env фикстуре
func TestGeneric_UpdateVersion_Env_RoundTrip(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "images.env")

	newRef := mustRef("docker.io", "library/debian", "12.2.0")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := s.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs after update, got %d", len(refs))
	}
	assertRef(t, refs[0], "docker.io", "library/debian", "12.2.0") // обновлён
	assertRef(t, refs[1], "ghcr.io", "org/tool", "3.0.1")          // не тронут
}

// generic: patch-обновление на YAML
func TestGeneric_UpdateVersion_PatchBump(t *testing.T) {
	s := defaultGenericStrategy(t)
	content := readFixture(t, "docker-compose.yml") // nginx:1.25.3

	newRef := mustRef("docker.io", "library/nginx", "1.25.9")
	updated, err := s.UpdateVersion(content, newRef)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("nginx:1.25.9")) {
		t.Errorf("patch not updated:\n%s", updated)
	}
	// myapp не тронут
	if !bytes.Contains(updated, []byte("myorg/myapp:2.1.0")) {
		t.Errorf("unrelated image modified:\n%s", updated)
	}
}
