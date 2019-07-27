package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fiws/minepkg/internals/minecraft"

	"github.com/fiws/minepkg/pkg/manifest"
)

var (
	// ErrLaunchNotImplemented is returned if attemting to start a non vanilla instance
	ErrLaunchNotImplemented = errors.New("Can only launch vanilla & fabric instances (for now)")
	// ErrNoCredentials is returned when an instance is launched without `MojangProfile` beeing set
	ErrNoCredentials = errors.New("Can not launch without mojang credentials")
	// ErrNoPaidAccount is returned when an instance is launched without `MojangProfile` beeing set
	ErrNoPaidAccount = errors.New("You need to buy Minecraft to launch it")
)

// GetLaunchManifest returns the merged manifest for the instance
func (i *Instance) GetLaunchManifest() (*minecraft.LaunchManifest, error) {
	man, err := i.launchManifest()
	if err != nil {
		return nil, err
	}

	if man.InheritsFrom != "" {
		parent, err := i.getVanillaManifest(man.InheritsFrom)
		if err != nil {
			return nil, err
		}
		man.MergeWith(parent)
	}
	return man, nil
}

// LaunchOptions are options for launching
type LaunchOptions struct {
	LaunchManifest *minecraft.LaunchManifest
	// SkipDownload will NOT download missing assets & libraries
	SkipDownload bool
	// Offline is not implemented
	Offline    bool
	Java       string
	Server     bool
	JoinServer string
}

// Launch starts the minecraft instance
func (i *Instance) Launch(opts *LaunchOptions) error {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	creds := i.MojangCredentials
	if creds == nil {
		return ErrNoCredentials
	}

	profile := creds.SelectedProfile
	// do not allow non paid accounts to start minecraft
	// (demo mode is not implemented)
	if profile == nil || profile.Paid != true {
		return ErrNoPaidAccount
	}

	// this file tells us howto construct the start command
	launchManifest := opts.LaunchManifest

	// get manifest if not passed as option
	if launchManifest == nil {
		launchManifest, err = i.GetLaunchManifest()
		if err != nil {
			return err
		}
	}

	// ensure some java binary is set
	if opts.Java == "" {
		opts.Java = i.javaBin()
		// no local java installation
		if opts.Java == "" {
			// download java
			if err := i.UpdateJava(); err != nil {
				return err
			}
			opts.Java = i.javaBinary
		}
	}

	// Download assets if not skipped
	if opts.SkipDownload != true {
		i.ensureAssets(launchManifest)
	}

	// create tmp dir for instance
	tmpName := i.Manifest.Package.Name + fmt.Sprintf("%d", time.Now().Unix())
	tmpDir, err := ioutil.TempDir("", tmpName)
	if err != nil {
		return err
	}

	defer os.RemoveAll(tmpDir) // cleanup dir after minecraft is closed
	libDir := filepath.Join(i.LibrariesDir())

	// build that spooky -cp arg
	var cpArgs []string

	libs := launchManifest.Libraries.Required()

	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "macos"
	}

	for _, lib := range libs {

		if opts.SkipDownload != true {
			// TODO: replace with method
			existOrDownload(lib)
		}

		// copy natives. not sure if this implementation is complete
		if len(lib.Natives) != 0 {
			// extract native to temp dir
			nativeID, _ := lib.Natives[osName]
			native := lib.Downloads.Classifiers[nativeID]

			p := filepath.Join(libDir, native.Path)

			err := extractNative(p, tmpDir)
			if err != nil {
				return err
			}
			cpArgs = append(cpArgs, filepath.Join(libDir, native.Path))
		} else {
			// append this library to our doom -cp arg
			libPath := lib.Filepath()
			cpArgs = append(cpArgs, filepath.Join(libDir, libPath))
		}
	}

	// finally append the minecraft.jar
	jarTarget := launchManifest.Jar
	if jarTarget == "" {
		jarTarget = launchManifest.InheritsFrom
	}
	if jarTarget == "" {
		jarTarget = launchManifest.ID
	}
	mcJar := filepath.Join(i.VersionsDir(), jarTarget, jarTarget+".jar")
	cpArgs = append(cpArgs, mcJar)

	replacer := strings.NewReplacer(
		v("auth_player_name"), profile.Name,
		v("version_name"), jarTarget,
		v("game_directory"), cwd,
		v("assets_root"), filepath.Join(i.AssetsDir()),
		v("assets_index_name"), launchManifest.Assets, // asset index version
		v("auth_uuid"), profile.ID, // profile id
		v("auth_access_token"), creds.AccessToken,
		v("user_type"), "mojang", // unsure about this one (legacy mc login flag?)
		v("version_type"), launchManifest.Type, // release / snapshot … etc
	)

	args := replacer.Replace(launchManifest.LaunchArgs())

	javaCpSeperator := ":"
	// of course
	if runtime.GOOS == "windows" {
		javaCpSeperator = ";"
	}

	cmdArgs := []string{
		"-Xss1M",
		"-Djava.library.path=" + tmpDir,
		"-Dminecraft.launcher.brand=minepkg",
		// "-Dminecraft.launcher.version=" + "0.0.2", // TODO: implement!
		"-Dminecraft.client.jar=" + mcJar,
		"-cp",
		strings.Join(cpArgs, javaCpSeperator),
		// "-Xmx2G", // TODO: option!
		"-XX:+UnlockExperimentalVMOptions",
		"-XX:+UseG1GC",
		"-XX:G1NewSizePercent=20",
		"-XX:G1ReservePercent=20",
		"-XX:MaxGCPauseMillis=50",
		"-XX:G1HeapRegionSize=32M",
		launchManifest.MainClass,
	}

	if opts.Server == false {
		cmdArgs = append(cmdArgs, strings.Split(args, " ")...)
	} else {
		cmdArgs = append(cmdArgs, "nogui")
	}

	// fmt.Println("cmd: ")
	// fmt.Println(cmdArgs)
	// fmt.Println("tmpdir: + " + tmpDir)
	// os.Exit(0)

	if opts.Java == "" {
		opts.Java = "java"
	}
	cmd := exec.Command(opts.Java, cmdArgs...)

	if opts.JoinServer != "" {
		cmd.Env = append(os.Environ(), "MINEPKG_COMPANION_PLAY=server://"+opts.JoinServer)
	}

	if opts.Server == true {
		cmd.Stdin = os.Stdin
	}

	// TODO: detatch from process
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	return err
}

func (i *Instance) launchManifest() (*minecraft.LaunchManifest, error) {
	lockfile := i.Lockfile
	if lockfile == nil {
		i.initLockfile()
	}
	buf, err := ioutil.ReadFile(filepath.Join(i.VersionsDir(), lockfile.McManifestName()))
	if err == nil {
		man := minecraft.LaunchManifest{}
		json.Unmarshal(buf, &man)
		return &man, nil
	}

	switch i.Platform() {
	case PlatformFabric:
		return i.fetchFabricManifest(lockfile.Fabric)
	case PlatformForge:
		// TODO: forge
		panic("Forge is not supported")
	default:
		return i.getVanillaManifest(i.Manifest.Requirements.Minecraft)
	}
}

func (i *Instance) getVanillaManifest(v string) (*minecraft.LaunchManifest, error) {
	buf, err := ioutil.ReadFile(filepath.Join(i.VersionsDir(), v, v+".json"))
	if err != nil {
		return i.fetchVanillaManifest(v)
		// return nil, err
	}
	instructions := minecraft.LaunchManifest{}
	json.Unmarshal(buf, &instructions)
	return &instructions, nil
}

func (i *Instance) fetchFabricManifest(lock *manifest.FabricLock) (*minecraft.LaunchManifest, error) {
	manifest := minecraft.LaunchManifest{}
	loader := lock.FabricLoader
	mappings := lock.Mapping
	minecraft := lock.Minecraft

	version := minecraft + "-fabric-" + loader
	dir := filepath.Join(i.VersionsDir(), i.Manifest.Requirements.Minecraft+"-fabric-"+loader)
	file := filepath.Join(dir, version+".json")

	// cached
	if rawMan, err := ioutil.ReadFile(file); err == nil {
		err := json.Unmarshal(rawMan, &manifest)
		if err != nil {
			return nil, err
		}
		return &manifest, nil
	}

	res, err := http.Get("https://fabricmc.net/download/vanilla?format=profileJson&loader=" + url.QueryEscape(loader) + "&yarn=" + url.QueryEscape(mappings))
	if err != nil {
		return nil, err
	}

	buf, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		return nil, err
	}
	ioutil.WriteFile(filepath.Join(dir, version+".json"), buf, 0666)

	if err = json.Unmarshal(buf, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (i *Instance) fetchVanillaManifest(version string) (*minecraft.LaunchManifest, error) {
	mcVersions, err := GetMinecraftReleases(context.TODO())
	if err != nil {
		return nil, err
	}

	manifestURL := ""
	for _, mc := range mcVersions.Versions {
		if mc.ID == version {
			manifestURL = mc.URL
		}
	}
	if manifestURL == "" {
		return nil, ErrorNoVersion
	}

	manifest := minecraft.LaunchManifest{}
	res, err := http.Get(manifestURL)
	if err != nil {
		return nil, err
	}

	buf, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(i.VersionsDir(), version)
	os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		return nil, err
	}
	ioutil.WriteFile(filepath.Join(dir, version+".json"), buf, 0666)

	if err = json.Unmarshal(buf, &manifest); err != nil {
		return nil, err
	}

	// TODO: this is a side effect. it should not be here
	jarRes, err := http.Get(manifest.Downloads.Client.URL)
	if err != nil {
		return nil, err
	}
	jarDest, err := os.Create(filepath.Join(dir, version+".jar"))
	if err != nil {
		return nil, err
	}

	// copy the jar
	if _, err = io.Copy(jarDest, jarRes.Body); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// FindMissingLibraries returns all missing assets
func (i *Instance) FindMissingLibraries(man *minecraft.LaunchManifest) (minecraft.Libraries, error) {
	missing := minecraft.Libraries{}

	libs := man.Libraries.Required()
	globalDir := i.LibrariesDir()

	for _, lib := range libs {
		path := filepath.Join(globalDir, lib.Filepath())
		if _, err := os.Stat(path); err == nil {
			continue
		}

		missing = append(missing, lib)
	}

	return missing, nil
}

// FindMissingAssets returns all missing assets
func (i *Instance) FindMissingAssets(man *minecraft.LaunchManifest) ([]minecraft.AssetObject, error) {
	assets := minecraft.AssetIndex{}

	assetJSONPath := filepath.Join(i.AssetsDir(), "indexes", man.Assets+".json")
	buf, err := ioutil.ReadFile(assetJSONPath)
	if err != nil {
		res, err := http.Get(man.AssetIndex.URL)
		if err != nil {
			return nil, err
		}

		buf, err = ioutil.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}

		os.MkdirAll(filepath.Join(i.AssetsDir(), "indexes"), os.ModePerm)
		err = ioutil.WriteFile(assetJSONPath, buf, 0666)
		if err != nil {
			return nil, err
		}
	}
	json.Unmarshal(buf, &assets)

	missing := make([]minecraft.AssetObject, 0)

	for _, asset := range assets.Objects {
		file := filepath.Join(i.AssetsDir(), "objects", asset.UnixPath())
		if _, err := os.Stat(file); os.IsNotExist(err) {
			missing = append(missing, asset)
		}
	}

	return missing, nil
}
