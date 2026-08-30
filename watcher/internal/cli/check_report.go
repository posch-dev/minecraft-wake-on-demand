package cli

import (
	"fmt"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

func (c *checker) section(title string) {
	fmt.Printf("\n%s\n", title)
}

func (c *checker) ok(format string, args ...any) {
	fmt.Printf("  ok    %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) fail(format string, args ...any) {
	c.failures++
	ui.PrintError("  FAIL  " + fmt.Sprintf(format, args...))
}

func (c *checker) warn(format string, args ...any) {
	c.warnings++
	ui.PrintWarning("  warn  " + fmt.Sprintf(format, args...))
}

func (c *checker) info(format string, args ...any) {
	fmt.Println(ui.Hint("  ..    " + fmt.Sprintf(format, args...)))
}

func (c *checker) hint(format string, args ...any) {
	fmt.Println(ui.Hint("        " + fmt.Sprintf(format, args...)))
}
