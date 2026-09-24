package maintenance

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLegacyDatabaseDescriptorBlocksMaintenance(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("host process inspection requires root")
	}
	database := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(database, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", "exec 3< \"$1\"; echo ready; read end", "sh", database)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	defer stdin.Close()
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err = CheckDatabaseUsers(database); err == nil {
		t.Fatal("legacy daemon descriptor was not detected")
	}
	stdin.Close()
	command.Wait()
	if err = CheckDatabaseUsers(database); err != nil {
		t.Fatal(err)
	}
}
