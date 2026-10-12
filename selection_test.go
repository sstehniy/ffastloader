//go:build !windows

package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestSelectionTerminal(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the terminal regression test")
	}
	if _, err := exec.LookPath("tput"); err != nil {
		t.Skip("tput is required for the terminal regression test")
	}
	cmd := exec.Command(python, "-c", `
import errno, os, pty, select, sys, time

if os.environ.get("TERM", "dumb") in ("", "dumb"):
    os.environ["TERM"] = "xterm-256color"

for action in ("navigate", "escape", "quit"):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ["FFASTLOADER_SELECTION_TEST"] = action
        os.execv(sys.argv[1], [sys.argv[1], "-test.run=^TestSelectionTerminalHelper$", "-test.v"])
    output = bytearray()
    sent = False
    application_mode = False
    deadline = time.monotonic() + 10
    try:
        while time.monotonic() < deadline:
            ready, _, _ = select.select([fd], [], [], 0.1)
            if not ready:
                continue
            try:
                data = os.read(fd, 65536)
            except OSError as e:
                if e.errno == errno.EIO:
                    break
                raise
            if not data:
                break
            output.extend(data)
            if b"\x1b[?1h" in data:
                application_mode = True
            if b"Currently selected:" in output and not sent:
                prefix = b"\x1bO" if application_mode else b"\x1b["
                if action == "navigate":
                    keys = prefix + b"B " + prefix + b"A " + prefix + b"B" + prefix + b"B\ry"
                elif action == "escape":
                    keys = b"\x1b"
                else:
                    keys = b"q"
                os.write(fd, keys)
                sent = True
        else:
            raise AssertionError("menu did not finish before timeout")
        _, status = os.waitpid(pid, 0)
        pid = 0
        assert status == 0 and b"PASS" in output, output.decode(errors="replace")
        if application_mode:
            assert b"\x1b[?1l" in output, "terminal cursor mode was not restored"
    finally:
        os.close(fd)
        if pid:
            os.kill(pid, 9)
            os.waitpid(pid, 0)
`, os.Args[0])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal regression: %v\n%s", err, output)
	}
}

func TestSelectionTerminalHelper(t *testing.T) {
	action := os.Getenv("FFASTLOADER_SELECTION_TEST")
	if action == "" {
		t.Skip("runs inside TestSelectionTerminal's pseudo-terminal")
	}
	groups := interactiveSelection([]FileGroup{
		{Name: "first", Files: []string{"first.rar"}, Selected: true},
		{Name: "second", Files: []string{"second.rar"}, Selected: true},
		{Name: "third", Files: []string{"third.rar"}, Selected: true},
	})
	if action == "navigate" {
		if len(groups) != 3 || groups[0].Selected || groups[1].Selected || !groups[2].Selected {
			t.Fatalf("arrows and Space produced the wrong selection: %+v", groups)
		}
	} else if len(groups) != 0 {
		t.Fatalf("cancellation returned files to download: %+v", groups)
	}
}
