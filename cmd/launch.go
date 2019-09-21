package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fiws/minepkg/cmd/launch"
	"github.com/fiws/minepkg/internals/instances"
	"github.com/fiws/minepkg/internals/minecraft"
	"github.com/fiws/minepkg/pkg/api"
	"github.com/spf13/cobra"
)

var (
	version       string
	serverMode    bool
	useSystemJava bool
	debugMode     bool
	offlineMode   bool
	acceptEula    bool
	onlyPrepare   bool
	crashTest     bool
)

func init() {
	launchCmd.Flags().BoolVarP(&serverMode, "server", "s", false, "Start a server instead of a client")
	launchCmd.Flags().BoolVarP(&debugMode, "debug", "", false, "Do not start, just debug")
	launchCmd.Flags().BoolVarP(&useSystemJava, "system-java", "", false, "Use system java instead of internal installation")
	launchCmd.Flags().BoolVarP(&offlineMode, "offline", "", false, "Start the server in offline mode (server only)")
	launchCmd.Flags().BoolVarP(&acceptEula, "accept-eula", "a", false, "Accept the mojang eula. See https://account.mojang.com/documents/minecraft_eula")
	launchCmd.Flags().BoolVarP(&onlyPrepare, "only-prepare", "", false, "Only prepare, skip launching.")
	launchCmd.Flags().BoolVarP(&crashTest, "crashtest", "", false, "Stop server after it's online (can be used for testing)")
	rootCmd.AddCommand(launchCmd)
}

var launchCmd = &cobra.Command{
	Use:   "launch [modpack]",
	Short: "Launch a local or remote modpack.",
	Long: `If a modpack name or URL is supplied, that modpack will be launched.
	Alternativly: Can be used in directories containing a minepkg.toml manifest to launch that modpack.
	`, // TODO
	Aliases: []string{"run", "start", "play"},
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var instance *instances.Instance
		var instanceDir string

		if len(args) == 0 {
			var err error
			wd, _ := os.Getwd()
			instance, err = instances.DetectInstance()
			instanceDir = wd

			if err != nil {
				logger.Fail("Instance problem: " + err.Error())
			}
			instance.MinepkgAPI = apiClient
		} else {
			reqs := &api.RequirementQuery{
				Plattform: "fabric", // TODO: not static!
				Minecraft: "*",
				Version:   "latest", // TODO: get from id
			}
			release, err := apiClient.FindRelease(context.TODO(), args[0], reqs)
			if err != nil {
				logger.Fail(err.Error())
			}

			// TODO: check if exists
			// TODO: check error
			instance = &instances.Instance{
				GlobalDir:  globalDir,
				Manifest:   release.Manifest,
				MinepkgAPI: apiClient,
			}

			instanceDir = filepath.Join(instance.InstancesDir(), release.Package.Name+"@"+release.Package.Platform)
			os.MkdirAll(instanceDir, os.ModePerm)
			wd, err := os.Getwd()
			if err != nil {
				logger.Fail(err.Error())
			}
			// change dir to the instance
			os.Chdir(instanceDir)
			// back to current directory after minecraft stops
			defer os.Chdir(wd)

			instance.ModsDirectory = filepath.Join(instanceDir, "mods")

			// TODO: only show when there actually is a update. ask user?
			logger.Headline("Updating instance")
			// maybe not update requirements every time
			if err := instance.UpdateLockfileRequirements(context.TODO()); err != nil {
				logger.Fail(err.Error())
			}
			if err := instance.UpdateLockfileDependencies(context.TODO()); err != nil {
				logger.Fail(err.Error())
			}

			instance.SaveManifest()
			instance.SaveLockfile()
		}

		switch {
		case crashTest && serverMode != true:
			logger.Fail("Can only crashtest servers. append --server to crashtest")
		case instance.Manifest.Package.Type != "modpack":
			logger.Fail("Can only launch modpacks. You can use \"minepkg try\" if you want to test a mod.")
		case instance.Manifest.PlatformString() == "forge":
			logger.Fail("Can not launch forge modpacks for now. Sorry.")
		}

		if useSystemJava == true {
			instance.UseSystemJava()
		}

		// launch instance
		fmt.Printf("Launching %s\n", instance.Desc())
		fmt.Printf("Instance location: %s\n", instanceDir)

		// we need login credentials to launch the client
		// the server needs no creds
		if serverMode != true {
			creds, err := ensureMojangAuth()
			if err != nil {
				logger.Fail(err.Error())
			}
			instance.MojangCredentials = creds.Mojang
		}

		cliLauncher := launch.CLILauncher{Instance: instance, ServerMode: serverMode}

		if err := cliLauncher.Prepare(); err != nil {
			logger.Fail(err.Error())
		}

		launchManifest := cliLauncher.LaunchManifest

		// TODO: This is just a hack
		if serverMode == true {
			launchManifest.MainClass = strings.Replace(launchManifest.MainClass, "Client", "Server", -1)

			// TODO: ASK USER!!! this is a publish blocker!
			eula := "# generated by minepkg\n# https://account.mojang.com/documents/minecraft_eula\neula=true\n"
			ioutil.WriteFile("./eula.txt", []byte(eula), os.ModePerm)

			// register server if this manifest is not local without a version
			if instance.Manifest.Package.Version != "" && offlineMode != true {
				id := instance.Manifest.Package.Name + "@" + instance.Manifest.Package.Version
				data, _ := json.Marshal(&MinepkgMapping{instance.Manifest.PlatformString(), id})

				req, _ := http.NewRequest("POST", "https://test-api.minepkg.io/v1/server-mappings", bytes.NewBuffer(data))
				apiClient.DecorateRequest(req)
				_, err := apiClient.HTTP.Do(req)
				if err != nil {
					fmt.Println("Could not register server on minepkg.io – try again later")
				} else {
					// TODO: fill in ip/host
					logger.Info("Registered server on minepkg.io. Join without setup using \"minepkg join <ip/host>\"")
				}
			}

			if offlineMode == true {
				settingsFile := filepath.Join(instanceDir, "server.properties")
				logger.Log("Starting server in offline mode")
				rawSettings, err := ioutil.ReadFile(settingsFile)

				// workarround to get server that was started in offline mode for the first time
				// to start in online mode next time it is launched
				if err != nil {
					rawSettings = []byte("online-mode=true\n")
				}
				// write back old config after we are done
				// TODO: this is too unsafe – crashes or panics will prevent this!
				defer ioutil.WriteFile(settingsFile, rawSettings, os.ModePerm)

				settings := minecraft.ParseServerProps(rawSettings)
				settings["online-mode"] = "false"

				// write modified config file
				if err := ioutil.WriteFile(settingsFile, []byte(settings.String()), os.ModePerm); err != nil {
					panic(err)
				}
			}
		}

		if onlyPrepare == true {
			fmt.Println("Skipping launch as requested")
			os.Exit(0)
		}

		fmt.Println("\nLaunching Minecraft …")
		opts := &instances.LaunchOptions{
			LaunchManifest: launchManifest,
			SkipDownload:   true,
			Server:         serverMode,
			Debug:          debugMode,
		}

		launchErr := make(chan error)
		crashErr := make(chan error)

		if crashTest == true {

			go func() {
				tries := 0

				// try to connect every 3 seconds for 10 times (30 seconds to start server)
				for {
					tries++
					time.Sleep(3 * time.Second)
					// TODO: no hardcoded port
					conn, err := net.DialTimeout("tcp", ":25565", time.Duration(120)*time.Second)

					// no error, close connection and send nil in err channel
					if err == nil {
						defer conn.Close()
						crashErr <- nil
						break
					}

					//  could not connect, can we try again? send error in channel otherwise
					if tries >= 10 {
						crashErr <- err
						break
					}
				}
			}()
		}

		go func() {
			// finally, start the instance
			if err := cliLauncher.Launch(opts); err != nil {
				launchErr <- err
			}
			launchErr <- nil
		}()

		stopAfterCrashtest := func() {
			p, err := os.FindProcess(cliLauncher.Cmd.Process.Pid)
			if err != nil {
				fmt.Println("Could not stop minecraft after crashtest. Its'probably already stopped … which is not good")
				os.Exit(1)
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				p.Signal(syscall.SIGKILL)
			}

			select {
			case <-launchErr:
				return
			case <-time.After(5 * time.Second):
				fmt.Println("Timed out stopping minecraft. Killing it")
				if err := p.Signal(syscall.SIGKILL); err != nil {
					fmt.Println("Could not kill minecraft")
				}
			}
		}

		select {
		// normal launch & minecraft was stopped
		case err := <-launchErr:
			if err != nil {
				// TODO: this stops any defer from running !!!
				logger.Fail(err.Error())
			}
		// crashtest and we got a response from the crash go routine
		case err := <-crashErr:
			// stop the minecraft server, crashtest went well or timed out
			if err != nil {
				fmt.Printf("Crashtest: could not connect to server (%s)\n", err)
				stopAfterCrashtest()
				os.Exit(69)
			}
			fmt.Println("Crashtest went fine! Waiting for server to shut down")
			stopAfterCrashtest()
			// normal exit
		}

	},
}
