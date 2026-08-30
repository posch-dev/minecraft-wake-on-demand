package compose

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
	"gopkg.in/yaml.v3"
)

const foreignCompose = `# Someone else's stack, do not break it.
services:
  # the thing that serves my website
  caddy:
    image: caddy:2
    ports:
      - "80:80"
    volumes:
      - ./caddy:/data

  postgres:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: "${DB_PASSWORD}"

volumes:
  pgdata: {}
`

func testSpec() ComposeSpec {
	spec := DefaultComposeSpec("minecraft", 25565)
	spec.MCVersion = "1.21.4"
	return spec
}

func parseCompose(t *testing.T, body string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("the result is not valid YAML: %v\n%s", err, body)
	}
	return parsed
}

func services(t *testing.T, body string) map[string]any {
	t.Helper()
	parsed := parseCompose(t, body)
	found, ok := parsed["services"].(map[string]any)
	if !ok {
		t.Fatalf("no services mapping:\n%s", body)
	}
	return found
}

func TestNewComposeFileHasBothServices(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}

	found := services(t, body)
	if _, ok := found["minecraft"]; !ok {
		t.Errorf("no minecraft service:\n%s", body)
	}
	if _, ok := found["minecraft-backup"]; !ok {
		t.Errorf("no backup service:\n%s", body)
	}
}

// RCON on the LAN would be a remote console with one password in front of it.
func TestNewComposeFilePublishesOnlyTheGamePort(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(body, `"25565:25565"`) {
		t.Errorf("the game port is not published:\n%s", body)
	}
	if strings.Contains(body, "25575") {
		t.Errorf("RCON must never be published:\n%s", body)
	}
}

// The watcher starts the container, docker must not race it on boot.
func TestNewComposeFileDoesNotRestartOnItsOwn(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `restart: "no"`) {
		t.Errorf("restart is not disabled:\n%s", body)
	}
	for _, want := range []string{"AUTOPAUSE", "ENABLE_RCON", "EULA"} {
		if !strings.Contains(body, want) {
			t.Errorf("the file is missing %s:\n%s", want, body)
		}
	}
}

// The password goes into .env under our own name, never inline.
func TestComposeReferencesThePasswordVariable(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "${"+RconPasswordVar) {
		t.Errorf("the compose file does not read %s:\n%s", RconPasswordVar, body)
	}
}

func TestBackupServiceCanBeLeftOut(t *testing.T) {
	spec := testSpec()
	spec.Backups = false

	body, err := NewComposeFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := services(t, body)["minecraft-backup"]; ok {
		t.Errorf("the backup service should be absent:\n%s", body)
	}
}

// The whole point of editing the node tree instead of rewriting the file.
func TestAppendingKeepsForeignServicesAndComments(t *testing.T) {
	body, err := AddServicesToCompose(foreignCompose, testSpec())
	if err != nil {
		t.Fatal(err)
	}

	found := services(t, body)
	for _, name := range []string{"caddy", "postgres", "minecraft", "minecraft-backup"} {
		if _, ok := found[name]; !ok {
			t.Errorf("service %q is missing after the append:\n%s", name, body)
		}
	}
	for _, comment := range []string{
		"# Someone else's stack, do not break it.",
		"# the thing that serves my website",
	} {
		if !strings.Contains(body, comment) {
			t.Errorf("the append ate the comment %q:\n%s", comment, body)
		}
	}
	if !strings.Contains(body, "${DB_PASSWORD}") {
		t.Errorf("a foreign variable reference was mangled:\n%s", body)
	}
	if _, ok := parseCompose(t, body)["volumes"]; !ok {
		t.Errorf("the top level volumes key was dropped:\n%s", body)
	}
}

// Overwriting somebody's own minecraft service is the one unrecoverable move.
func TestAppendingRefusesAnExistingServiceName(t *testing.T) {
	existing := "services:\n  minecraft:\n    image: something/else\n"

	_, err := AddServicesToCompose(existing, testSpec())
	if err == nil {
		t.Fatal("a name collision must be refused")
	}
	if !strings.Contains(err.Error(), "minecraft") {
		t.Errorf("the error should name the service, got: %v", err)
	}
}

func TestAppendingRefusesTheBackupNameToo(t *testing.T) {
	existing := "services:\n  minecraft-backup:\n    image: something/else\n"

	if _, err := AddServicesToCompose(existing, testSpec()); err == nil {
		t.Error("a collision on the backup name must be refused as well")
	}
}

func TestAppendingCreatesAMissingServicesKey(t *testing.T) {
	body, err := AddServicesToCompose("volumes:\n  pgdata: {}\n", testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := services(t, body)["minecraft"]; !ok {
		t.Errorf("the service was not added:\n%s", body)
	}
}

func TestAppendingRefusesRubbish(t *testing.T) {
	for _, existing := range []string{"", "just a string", "- a\n- list\n"} {
		if _, err := AddServicesToCompose(existing, testSpec()); err == nil {
			t.Errorf("addServicesToCompose(%q) should have failed", existing)
		}
	}
}

// Exactly the three lines, appended, so a foreign .env keeps working.
func TestAppendRCONPasswordAddsThreeLines(t *testing.T) {
	existing := "DB_PASSWORD=hunter2\nTZ=Europe/Vienna\n"

	result := AppendRCONPassword(existing, "s3cret")

	if !strings.HasPrefix(result, existing) {
		t.Errorf("what was there must stay untouched:\n%s", result)
	}
	added := strings.TrimPrefix(result, existing)
	want := "\n# RCON password, added by Minecraft Wake-on-Demand\n" + RconPasswordVar + "=s3cret\n"
	if added != want {
		t.Errorf("appended %q, want %q", added, want)
	}
}

func TestAppendRCONPasswordHandlesAnEmptyFile(t *testing.T) {
	result := AppendRCONPassword("", "s3cret")

	if strings.HasPrefix(result, "\n\n") {
		t.Errorf("an empty .env should not gain two blank lines: %q", result)
	}
	if !strings.Contains(result, RconPasswordVar+"=s3cret") {
		t.Errorf("the password is missing: %q", result)
	}
}

func TestAppendRCONPasswordNormalisesAMissingNewline(t *testing.T) {
	result := AppendRCONPassword("TZ=Europe/Vienna", "s3cret")

	if !strings.Contains(result, "TZ=Europe/Vienna\n\n# RCON password") {
		t.Errorf("a file without a trailing newline was joined badly:\n%q", result)
	}
}

// Our own name, because a foreign .env may already define RCON_PASSWORD and the
// last definition would win.
func TestPasswordVariableDoesNotCollideWithTheCommonName(t *testing.T) {
	if RconPasswordVar == "RCON_PASSWORD" {
		t.Fatal("the variable has to be distinct from the name other stacks use")
	}
	if HasRCONPasswordVar("RCON_PASSWORD=somebody-elses\n") {
		t.Error("a foreign RCON_PASSWORD must not read as ours")
	}
	if !HasRCONPasswordVar("X=1\n" + RconPasswordVar + "=ours\n") {
		t.Error("our own variable was not recognised")
	}
	if HasRCONPasswordVar("# " + RconPasswordVar + "=commented-out\n") {
		t.Error("a commented line must not count as set")
	}
}

func TestGeneratedPasswordNeedsNoQuoting(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		password, err := GenerateRCONPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(password) < 24 {
			t.Errorf("password is only %d characters", len(password))
		}
		if strings.ContainsAny(password, " \t\n\"'$`\\#=") {
			t.Errorf("password %q needs quoting in .env or a shell", password)
		}
		if seen[password] {
			t.Fatal("the same password came back twice")
		}
		seen[password] = true
	}
}

// Two files naming the same images drift apart the moment one is bumped alone.
func TestGeneratorAgreesWithTheShippedCompose(t *testing.T) {
	shipped := testsupport.ReadRepoFile(t, "server", "docker-compose.yml")
	for _, image := range []string{minecraftImage, backupImage} {
		if !strings.Contains(shipped, image) {
			t.Errorf("server/docker-compose.yml does not use %s, bump both together", image)
		}
	}
}

// LATEST is what the audit flagged. A Java suffix is just as wrong, it ties
// the runtime to one Minecraft generation and breaks on the next.
var datedImagePin = regexp.MustCompile(`^itzg/[a-z-]+:[0-9]{4}[.][0-9]+[.][0-9]+$`)

func TestGeneratedComposePinsItsImages(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}

	for _, floating := range []string{"minecraft-server:latest", "mc-backup\n", "mc-backup\""} {
		if strings.Contains(body, floating) {
			t.Errorf("the generated file carries the floating tag %q:\n%s", floating, body)
		}
	}
	for _, image := range []string{minecraftImage, backupImage} {
		if !datedImagePin.MatchString(image) {
			t.Errorf("%q is not a dated pin without a java suffix", image)
		}
		if !strings.Contains(body, image) {
			t.Errorf("the generated file is missing %q:\n%s", image, body)
		}
	}
}

func TestServerTypeReachesTheComposeFile(t *testing.T) {
	spec := testSpec()
	spec.ServerType = "FABRIC"

	body, err := NewComposeFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `TYPE: "FABRIC"`) {
		t.Errorf("the server type was not written:\n%s", body)
	}
}

func TestDefaultSpecPinsAConcreteVersion(t *testing.T) {
	spec := DefaultComposeSpec("minecraft", 25565)

	if strings.EqualFold(spec.MCVersion, "LATEST") {
		t.Error("the default version must be concrete, LATEST moves on its own")
	}
	if !slices.Contains(ServerTypes, spec.ServerType) {
		t.Errorf("default server type %q is not one of the known types", spec.ServerType)
	}
}

// An enforced but empty whitelist produces a server nobody can join.
func TestEmptyWhitelistIsNotEnforced(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "ENFORCE_WHITELIST") {
		t.Errorf("no names given, the whitelist must not be switched on:\n%s", body)
	}
}

func TestWhitelistNamesAreWritten(t *testing.T) {
	spec := testSpec()
	spec.Whitelist = []string{"eliah", "someone", "third"}

	body, err := NewComposeFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`WHITELIST: "eliah,someone,third"`,
		`ENFORCE_WHITELIST: "TRUE"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the file is missing %s:\n%s", want, body)
		}
	}
}

// The admin stands on its own, a server with no whitelist still needs one.
func TestAdminIsWrittenWithoutAWhitelist(t *testing.T) {
	spec := testSpec()
	spec.Admin = "eliah"

	body, err := NewComposeFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `OPS: "eliah"`) {
		t.Errorf("the admin was not written:\n%s", body)
	}
	if strings.Contains(body, "ENFORCE_WHITELIST") {
		t.Errorf("an admin is not a whitelist:\n%s", body)
	}
}

func TestNoAdminWritesNoOps(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "OPS:") {
		t.Errorf("nobody was named, so OPS should be absent:\n%s", body)
	}
}

// init offers transfer mode, so the server it creates has to accept transfers,
// otherwise the tool hands players to a server that turns them away.
func TestGeneratedComposeAcceptsTransfers(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `ACCEPTS_TRANSFERS: "TRUE"`) {
		t.Errorf("the generated file does not accept transfers:\n%s", body)
	}
}

// Signed chat lets the client hold messages back and offer them for reporting,
// which is not what a server among friends is for.
func TestGeneratedComposeLeavesChatUnsigned(t *testing.T) {
	body, err := NewComposeFile(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `ENFORCE_SECURE_PROFILE: "FALSE"`) {
		t.Errorf("chat is still signed:\n%s", body)
	}
	// Accounts still have to be real, the two are unrelated.
	if !strings.Contains(body, `ONLINE_MODE: "TRUE"`) {
		t.Errorf("online mode was turned off with it:\n%s", body)
	}
}
