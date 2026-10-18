// Command windows-image assembles the unofficial Windows images (the agent, and the
// Rustic and tools images its helper containers run) without a
// Docker daemon, so one Windows runner can produce images for several Windows
// Server releases (a Windows host can only build for its own release or older
// ones under Hyper-V isolation).
//
//	windows-image build [-profile agent|rustic|tools] [-file layer/path=local/path ...] -base mcr.microsoft.com/windows/nanoserver:1809 -exe arcane-agent.exe -tag ghcr.io/o/i:t -out i.tar [-load-out i-load.tar]
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
	legacytarball "github.com/google/go-containerregistry/pkg/legacy/tarball"
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
	profileName := fs.String("profile", "agent", "image to build: agent, rustic or tools")
	exe := fs.String("exe", "out/arcane-agent.exe", "windows/amd64 executable the image runs (agent and rustic)")
	var extraFiles repeated
	fs.Var(&extraFiles, "file", "extra layer/path=local/path file (repeatable)")
	tag := fs.String("tag", "arcane-agent-windows:dev", "image name stored in the tarball")
	out := fs.String("out", "image.tar", "output tarball, read back by push")
	loadOut := fs.String("load-out", "", "optional legacy tarball for docker load on Windows")
	var labels repeated
	fs.Var(&labels, "label", "extra key=value label (repeatable)")
	_ = fs.Parse(args)
	prof, ok := profiles[*profileName]
	if !ok {
		log.Fatalf("unknown profile %q", *profileName)
	}

	img, err := crane.Pull(*base, crane.WithPlatform(windowsAMD64))
	if err != nil {
		log.Fatalf("pull %s: %v", *base, err)
	}

	var files [][2]string
	if prof.file != "" {
		files = append(files, [2]string{prof.file, *exe})
	}
	for _, f := range extraFiles {
		layerPath, localPath, ok := strings.Cut(f, "=")
		if !ok {
			log.Fatalf("invalid -file %q", f)
		}
		files = append(files, [2]string{layerPath, localPath})
	}
	if len(files) == 0 {
		log.Fatal("no file to add to the image")
	}
	layerTar, err := layerTarball(prof, files)
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
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	prof.configure(cfg)
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
	// The default tarball names the config "sha256:<hex>", which docker load rejects
	// on Windows because ':' is not allowed in file names; the legacy format does not.
	if *loadOut != "" {
		if err := writeLegacy(*loadOut, ref, img); err != nil {
			log.Fatalf("write %s: %v", *loadOut, err)
		}
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

func writeLegacy(path string, ref name.Reference, img v1.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := legacytarball.Write(ref, img, f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// profile is what one kind of image adds on top of the nanoserver base.
type profile struct {
	dirs      []string // directories created in the layer, with a trailing slash
	file      string   // layer path of the -exe executable, empty when only -file is used
	configure func(cfg *v1.Config)
}

var profiles = map[string]profile{
	"agent": {
		dirs: []string{"app/", "app/data/"},
		file: "app/arcane-agent.exe",
		configure: func(cfg *v1.Config) {
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
			// No VOLUME: Docker on Windows does not match it against a C:\app\data mount spelled
			// with another case, adds an anonymous volume on the same path, and HCS refuses to start.
			cfg.Volumes = nil
			cfg.Labels["com.getarcaneapp.arcane.agent"] = "true"
		},
	},
	// Same contract as ghcr.io/rustic-rs/rustic: the entrypoint is rustic and Arcane
	// passes its arguments as the command.
	"rustic": {
		dirs: []string{"rustic/"},
		file: "rustic/rustic.exe",
		configure: func(cfg *v1.Config) {
			cfg.WorkingDir = `C:\rustic`
			cfg.Entrypoint = []string{`C:\rustic\rustic.exe`}
			cfg.Cmd = nil
			// Backed up volumes and the repository volume grant full access to administrators.
			cfg.User = "ContainerAdministrator"
			cfg.Volumes = nil
		},
	},
	// Windows counterpart of ghcr.io/getarcaneapp/tools: BusyBox applets and acfs in
	// C:\tools\bin (no trivy yet). Arcane runs "sleep infinity" and execs commands by name.
	"tools": {
		dirs: []string{"tools/", "tools/bin/", "tmp/", "root/", "root/.cache/"},
		configure: func(cfg *v1.Config) {
			cfg.Env = append(cfg.Env, `PATH=C:\tools\bin;C:\Windows\system32;C:\Windows`)
			cfg.WorkingDir = `C:\`
			cfg.Entrypoint = nil
			cfg.Cmd = []string{"sleep", "infinity"}
			// Volume contents grant full access to administrators.
			cfg.User = "ContainerAdministrator"
			cfg.Volumes = nil
		},
	},
}

// layerTarball writes the profile's directories and the files ([layer path, local path]) to a temporary tar.
func layerTarball(prof profile, files [][2]string) (string, error) {
	f, err := os.CreateTemp("", "arcane-layer-*.tar")
	if err != nil {
		return "", err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	now := time.Now()
	for _, dir := range prof.dirs {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o755, ModTime: now}); err != nil {
			return "", err
		}
	}
	for _, file := range files {
		if err := addFile(tw, file[0], file[1], now); err != nil {
			return "", err
		}
	}
	return f.Name(), tw.Close()
}

func addFile(tw *tar.Writer, layerPath, localPath string, modTime time.Time) error {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: layerPath, Mode: 0o755, Size: st.Size(), ModTime: modTime}); err != nil {
		return err
	}
	_, err = src.WriteTo(tw)
	return err
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
