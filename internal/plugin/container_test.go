package plugin

import (
	"strings"
	"testing"

	"example.com/pistos/internal/runner"
)

func TestRestrictedContainerArgs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-should-not-appear")
	spec := ContainerSpec{Runtime: &ContainerRuntime{Docker: "docker"}, Image: "img:1", Restricted: true, MemoryMB: 512, CPUs: 2,
		Mounts: []Mount{{Path: "/snap", ReadOnly: true}, {Path: "/scratch"}, {Path: "/snap", ReadOnly: true}}}
	args := strings.Join(spec.Args("pistos-x", runner.Cmd{Path: "/usr/bin/tool", Args: []string{"-a"}, Dir: "/snap/p", Env: []string{"GOFLAGS=-mod=readonly"}}), " ")
	for _, want := range []string{"--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--pids-limit", "--memory 512m", "--cpus 2",
		"--user ", "--pull never", "-v /snap:/snap:ro", "-v /scratch:/scratch:rw", "-w /snap/p", "-e GOFLAGS=-mod=readonly", "img:1 /usr/bin/tool -a"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Count(args, "/snap:/snap") != 1 || strings.Contains(args, "sk-should-not-appear") {
		t.Fatalf("duplicate mounts or leaked environment: %s", args)
	}
	trusted := strings.Join(ContainerSpec{Image: "img:1"}.Args("n", runner.Cmd{Path: "x"}), " ")
	if strings.Contains(trusted, "--network none") || strings.Contains(trusted, "--read-only") {
		t.Fatalf("trusted profile keeps network and writable root: %s", trusted)
	}
}
