package players

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const composeWithPlayers = `# my server
services:
  caddy:
    image: caddy:2

  minecraft:
    image: itzg/minecraft-server:2026.8.0-java21
    environment:
      EULA: "TRUE"
      OPS: "eliah"
      WHITELIST: "eliah,martin"
      ENFORCE_WHITELIST: "TRUE"
      MEMORY: "4G"
`

const composeWithoutWhitelist = `services:
  minecraft:
    image: itzg/minecraft-server:2026.8.0-java21
    environment:
      EULA: "TRUE"
      OPS: "eliah"
`

func TestReadPlayerList(t *testing.T) {
	list, err := ReadPlayerList(composeWithPlayers, "minecraft")
	if err != nil {
		t.Fatal(err)
	}
	if !list.Enforced {
		t.Error("the whitelist is enforced in the file")
	}
	if strings.Join(list.Whitelist, ",") != "eliah,martin" {
		t.Errorf("whitelist = %v", list.Whitelist)
	}
	if strings.Join(list.Admins, ",") != "eliah" {
		t.Errorf("admins = %v", list.Admins)
	}
}

// A list that is not enforced does nothing, so showing it would be a lie.
func TestUnenforcedWhitelistReadsAsEmpty(t *testing.T) {
	compose := strings.Replace(composeWithPlayers, `ENFORCE_WHITELIST: "TRUE"`, `ENFORCE_WHITELIST: "FALSE"`, 1)

	list, err := ReadPlayerList(compose, "minecraft")
	if err != nil {
		t.Fatal(err)
	}
	if list.Enforced || len(list.Whitelist) != 0 {
		t.Errorf("enforced = %v, whitelist = %v", list.Enforced, list.Whitelist)
	}
	if len(list.Admins) != 1 {
		t.Errorf("admins = %v, they exist without a whitelist", list.Admins)
	}
}

func TestWritePlayerListKeepsEverythingElse(t *testing.T) {
	list := List{Admins: []string{"eliah"}, Whitelist: []string{"eliah", "lena"}, Enforced: true}

	out, err := WritePlayerList(composeWithPlayers, "minecraft", list)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# my server", "caddy", `EULA: "TRUE"`, `MEMORY: "4G"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the edit lost %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `WHITELIST: "eliah,lena"`) {
		t.Errorf("the new list was not written:\n%s", out)
	}
}

// An empty key is a value to compose, so turning the list off has to remove it.
func TestTurningTheWhitelistOffRemovesTheKeys(t *testing.T) {
	list := List{Admins: []string{"eliah"}}

	out, err := WritePlayerList(composeWithPlayers, "minecraft", list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "WHITELIST") || strings.Contains(out, "ENFORCE_WHITELIST") {
		t.Errorf("the whitelist keys are still there:\n%s", out)
	}
	if !strings.Contains(out, `OPS: "eliah"`) {
		t.Errorf("the admin was dropped along with it:\n%s", out)
	}
}

func TestWritePlayerListAddsAnEnvironmentWhenThereIsNone(t *testing.T) {
	compose := "services:\n  minecraft:\n    image: itzg/minecraft-server:2026.8.0-java21\n"
	list := List{Admins: []string{"eliah"}}

	out, err := WritePlayerList(compose, "minecraft", list)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, out)
	}
	if !strings.Contains(out, `OPS: "eliah"`) {
		t.Errorf("the admin was not written:\n%s", out)
	}
}

func TestPlayerListRefusesAServiceThatIsNotThere(t *testing.T) {
	if _, err := ReadPlayerList(composeWithPlayers, "not-there"); err == nil {
		t.Error("a missing service should be reported, not invented")
	}
	if _, err := ReadPlayerList("just a string", "minecraft"); err == nil {
		t.Error("rubbish should be reported")
	}
}

func TestSplitNamesIgnoresSpacingAndBlanks(t *testing.T) {
	got := splitNames(" eliah , martin ,, lena ")

	if strings.Join(got, "|") != "eliah|martin|lena" {
		t.Errorf("splitNames = %v", got)
	}
	if len(splitNames("")) != 0 {
		t.Error("an empty value is no names at all")
	}
}

func TestWithoutNameIsCaseInsensitive(t *testing.T) {
	got := WithoutName([]string{"Eliah", "martin"}, "eliah")

	if strings.Join(got, ",") != "martin" {
		t.Errorf("withoutName = %v, Minecraft names are not case sensitive here", got)
	}
}

// Round trip, because the file this writes is the one it reads next time.
func TestPlayerListSurvivesAWriteAndRead(t *testing.T) {
	list := List{Admins: []string{"eliah", "lena"}, Whitelist: []string{"eliah", "lena", "martin"}, Enforced: true}

	out, err := WritePlayerList(composeWithoutWhitelist, "minecraft", list)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ReadPlayerList(out, "minecraft")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(again.Admins, ",") != "eliah,lena" {
		t.Errorf("admins came back as %v", again.Admins)
	}
	if strings.Join(again.Whitelist, ",") != "eliah,lena,martin" {
		t.Errorf("whitelist came back as %v", again.Whitelist)
	}
}
