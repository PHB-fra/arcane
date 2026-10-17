// Command windows-image assembles the unofficial Windows agent images without a
// Docker daemon, so one Windows runner can produce images for several Windows
// Server releases (a Windows host can only build for its own release or older
// ones under Hyper-V isolation).
//
//	windows-image build -base mcr.microsoft.com/windows/nanoserver:1809 -exe arcane-agent.exe -tag ghcr.io/o/i:t -out i.tar
//	windows-image push -tar i.tar -tag ghcr.io/o/i:t [-tag ghcr.io/o/i:t2]
//	windows-image index -tag ghcr.io/o/i:multi -from ghcr.io/o/i:a -from ghcr.io/o/i:b
package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

var windowsAMD64 = &v1.Platform{OS: "windows", Architecture: "amd64"}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal("usage: windows-image build|push|index [flags]")
	}
	switch os.Args[1] {
	case "build":
		build(os.Args[2:])
	case "push":
		push(os.Args[2:])
	case "index":
		index(os.Args[2:])
	default:
		log.Fatalf("unknown command %q", os.Args[1])
	}
}

func build(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	base := fs.String("base", "mcr.microsoft.com/windows/nanoserver:ltsc2022", "Windows base image")
	exe := fs.String("exe", "out/arcane-agent.exe", "agent built for windows/amd64")
	tag := fs.String("tag", "arcane-agent-windows:dev", "image name stored in the tarball")
	out := fs.String("out", "image.tar", "output tarball (docker load format)")
	var labels repeated
	fs.Var(&labels, "label", "extra key=value label (repeatable)")
	_ = fs.Parse(args)

	img, err := crane.Pull(*base, crane.WithPlatform(windowsAMD64))
	if err != nil {
		log.Fatalf("pull %s: %v", *base, err)
	}

	layerTar, err := layerTarball(*exe)
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(layerTar)

	// crane.Append rewrites the layer into the Windows layout (Files/, Hives/) for a Windows base.
	img, err = crane.Append(img, layerTar)
	if err != nil {
		log.Fatalf("append layer: %v", err)
	}

	cfgFile, err := img.ConfigFile()
	if err != nil {
		log.Fatal(err)
	}
	cfg := cfgFile.Config.DeepCopy()
	cfg.Env = append(cfg.Env,
		"ENVIRONMENT=production",
		"AGENT_MODE=true",
		"GIN_MODE=release",
		"PORT=3553",
		"DOCKER_HOST=npipe:////./pipe/docker_engine",
	)
	cfg.WorkingDir = `C:\app`
	cfg.Entrypoint = nil
	cfg.Cmd = []string{`C:\app\arcane-agent.exe`}
	// ContainerUser cannot open the Docker engine named pipe mounted into the container.
	cfg.User = "ContainerAdministrator"
	cfg.ExposedPorts = map[string]struct{}{"3553/tcp": {}}
	cfg.Volumes = map[string]struct{}{`C:\app\data`: {}}
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	cfg.Labels["com.getarcaneapp.arcane.agent"] = "true"
	cfg.Labels["org.opencontainers.image.base.name"] = *base
	for _, l := range labels {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			log.Fatalf("invalid label %q", l)
		}
		cfg.Labels[k] = v
	}

	img, err = mutate.Config(img, *cfg)
	if err != nil {
		log.Fatal(err)
	}
	img, err = mutate.CreatedAt(img, v1.Time{Time: time.Now().UTC()})
	if err != nil {
		log.Fatal(err)
	}

	ref, err := name.NewTag(*tag)
	if err != nil {
		log.Fatal(err)
	}
	if err := tarball.WriteToFile(*out, ref, img); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
	cfgFile, _ = img.ConfigFile()
	digest, _ := img.Digest()
	fmt.Printf("%s: %s (os.version %s, %s)\n", *out, *tag, cfgFile.OSVersion, digest)
}

// registryAuth uses REGISTRY_USERNAME/REGISTRY_PASSWORD when set, otherwise the Docker keychain.
func registryAuth() remote.Option {
	if pw := os.Getenv("REGISTRY_PASSWORD"); pw != "" {
		return remote.WithAuth(&authn.Basic{Username: os.Getenv("REGISTRY_USERNAME"), Password: pw})
	}
	return remote.WithAuthFromKeychain(authn.DefaultKeychain)
}

func push(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	tarPath := fs.String("tar", "image.tar", "tarball written by build")
	var tags repeated
	fs.Var(&tags, "tag", "destination tag (repeatable)")
	_ = fs.Parse(args)

	img, err := tarball.ImageFromPath(*tarPath, nil)
	if err != nil {
		log.Fatalf("read %s: %v", *tarPath, err)
	}
	digest, err := img.Digest()
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range tags {
		ref, err := name.NewTag(t)
		if err != nil {
			log.Fatal(err)
		}
		if err := remote.Write(ref, img, registryAuth()); err != nil {
			log.Fatalf("push %s: %v", t, err)
		}
		fmt.Printf("pushed %s@%s\n", t, digest)
	}
	setOutput("digest", digest.String())
}

func index(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	tag := fs.String("tag", "", "destination tag of the index")
	var from repeated
	fs.Var(&from, "from", "image to include (repeatable)")
	_ = fs.Parse(args)

	auth := registryAuth()
	idx := mutate.IndexMediaType(empty.Index, types.OCIImageIndex)
	for _, f := range from {
		ref, err := name.ParseReference(f)
		if err != nil {
			log.Fatal(err)
		}
		img, err := remote.Image(ref, auth)
		if err != nil {
			log.Fatalf("read %s: %v", f, err)
		}
		cfg, err := img.ConfigFile()
		if err != nil {
			log.Fatal(err)
		}
		// Windows hosts pick the entry whose os.version matches their own build.
		idx = mutate.AppendManifests(idx, mutate.IndexAddendum{
			Add: img,
			Descriptor: v1.Descriptor{Platform: &v1.Platform{
				OS: cfg.OS, Architecture: cfg.Architecture, OSVersion: cfg.OSVersion,
			}},
		})
		fmt.Printf("added %s (%s/%s %s)\n", f, cfg.OS, cfg.Architecture, cfg.OSVersion)
	}
	ref, err := name.NewTag(*tag)
	if err != nil {
		log.Fatal(err)
	}
	if err := remote.WriteIndex(ref, idx, auth); err != nil {
		log.Fatalf("push %s: %v", *tag, err)
	}
	digest, _ := idx.Digest()
	fmt.Printf("pushed %s@%s\n", *tag, digest)
	setOutput("digest", digest.String())
}

// layerTarball writes app/arcane-agent.exe and an empty app/data/ to a temporary tar.
func layerTarball(exe string) (string, error) {
	src, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "arcane-layer-*.tar")
	if err != nil {
		return "", err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	now := time.Now()
	for _, dir := range []string{"app/", "app/data/"} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o755, ModTime: now}); err != nil {
			return "", err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "app/arcane-agent.exe", Mode: 0o755, Size: st.Size(), ModTime: now}); err != nil {
		return "", err
	}
	if _, err := src.WriteTo(tw); err != nil {
		return "", err
	}
	return f.Name(), tw.Close()
}

// setOutput exposes a value to later GitHub Actions steps when run in a workflow.
func setOutput(key, value string) {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s=%s\n", key, value)
}
