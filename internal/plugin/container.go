package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/packs"
	"example.com/pistos/internal/runner"
)

// Container describes a pack's image: how to build it and where its tools live inside it.
type Container struct {
	Dockerfile string            `json:"dockerfile"`
	Files      []string          `json:"files"`
	Tools      map[string]string `json:"tools"`
}

func ImageTag(m *Manifest) string { return "pistos-pack-" + m.Name + ":" + m.Version }

// ContainerRuntime is the docker CLI plus the image IDs resolved for this run.
type ContainerRuntime struct {
	Docker string
	mu     sync.Mutex
	images map[string]string
}

func NewContainerRuntime() (*ContainerRuntime, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("execution.mode is docker but the docker CLI is not on PATH")
	}
	return &ContainerRuntime{Docker: docker, images: map[string]string{}}, nil
}

func (c *ContainerRuntime) docker(ctx context.Context, args ...string) (string, error) {
	res, err := runner.Run(ctx, runner.Cmd{Path: c.Docker, Args: args, Env: BaseEnv([]string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"}), Timeout: 30 * time.Minute})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("docker %s: %s", args[0], tail(strings.Split(string(res.Stderr)+string(res.Stdout), "\n")))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// ImageID returns the local image ID for the pack, or "" if it has not been built.
func (c *ContainerRuntime) ImageID(ctx context.Context, m *Manifest) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.images[m.Name]; ok {
		return id
	}
	id, err := c.docker(ctx, "image", "inspect", "--format", "{{.Id}}", ImageTag(m))
	if err != nil {
		return ""
	}
	c.images[m.Name] = id
	return id
}

// Build builds the pack image from the Dockerfile and worker sources embedded in pistos.
func (c *ContainerRuntime) Build(ctx context.Context, m *Manifest) (string, error) {
	if m.Container == nil {
		return "", fmt.Errorf("pack %s has no container image", m.Name)
	}
	dir, err := os.MkdirTemp("", "pistos-image-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for _, f := range append([]string{m.Container.Dockerfile}, m.Container.Files...) {
		b, err := fs.ReadFile(packs.FS, f)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			return "", err
		}
	}
	if _, err := c.docker(ctx, "build", "-q", "-t", ImageTag(m), dir); err != nil {
		return "", err
	}
	c.mu.Lock()
	delete(c.images, m.Name)
	c.mu.Unlock()
	if id := c.ImageID(ctx, m); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("image %s missing after build", ImageTag(m))
}

type Mount struct {
	Path     string
	ReadOnly bool
}

// ContainerSpec turns a host command into an equivalent `docker run`. Mounts keep their host paths,
// so analyzer output needs no path translation.
type ContainerSpec struct {
	Runtime    *ContainerRuntime
	Image      string
	Restricted bool
	Mounts     []Mount
	Env        map[string]string
	MemoryMB   int
	CPUs       int
}

// Args renders the docker run arguments. The restricted profile is the untrusted-analysis
// boundary: no network, read-only root filesystem, no capabilities, no privilege escalation,
// a non-root user and bounded resources.
func (s ContainerSpec) Args(name string, cmd runner.Cmd) []string {
	args := []string{"run", "--rm", "-i", "--init", "--name", name, "--pull", "never"}
	if runtime.GOOS != "windows" {
		args = append(args, "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()))
	}
	if s.Restricted {
		args = append(args, "--network", "none", "--read-only", "--tmpfs", "/tmp:rw,exec,size=2g",
			"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "1024")
	} else {
		args = append(args, "--tmpfs", "/tmp:rw,exec")
	}
	if s.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(s.MemoryMB)+"m")
	}
	if s.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(s.CPUs))
	}
	mounts := append([]Mount{}, s.Mounts...)
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Path < mounts[j].Path })
	seen := map[string]bool{}
	for _, m := range mounts {
		if m.Path == "" || seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		opt := "rw"
		if m.ReadOnly {
			opt = "ro"
		}
		args = append(args, "-v", m.Path+":"+m.Path+":"+opt)
	}
	for _, kv := range cmd.Env {
		args = append(args, "-e", kv)
	}
	if cmd.Dir != "" {
		args = append(args, "-w", cmd.Dir)
	}
	args = append(args, s.Image, cmd.Path)
	return append(args, cmd.Args...)
}

// Wrap returns cmd running inside the container. Killing the docker client does not stop the
// container, so Cleanup force-removes it after cancellation or timeout.
func (s ContainerSpec) Wrap(cmd runner.Cmd) runner.Cmd {
	name := "pistos-" + randomID()
	wrapped := runner.Cmd{
		Path: s.Runtime.Docker, Args: s.Args(name, cmd),
		Env:   BaseEnv([]string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"}),
		Stdin: cmd.Stdin, MaxOutput: cmd.MaxOutput, Timeout: cmd.Timeout,
	}
	docker := s.Runtime.Docker
	wrapped.Cleanup = func() {
		_ = exec.Command(docker, "rm", "-f", name).Run()
	}
	return wrapped
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// probeInContainer runs a version command inside the image without network or mounts.
func (c *ContainerRuntime) probe(ctx context.Context, image string, argv []string) (string, error) {
	res, err := runner.Run(ctx, runner.Cmd{
		Path: c.Docker, Env: BaseEnv([]string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"}), Timeout: 2 * time.Minute,
		Args: append([]string{"run", "--rm", "--network", "none", "--pull", "never", image}, argv...),
	})
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(append(res.Stdout, res.Stderr...))), nil
}
