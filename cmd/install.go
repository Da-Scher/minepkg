package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/fiws/minepkg/internals/instances"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(installCmd)
}

var installCmd = &cobra.Command{
	Use:     "install [name/url/id]",
	Short:   "installz packages",
	Long:    `Adds packages to your local modpack or mod. Launch the modpack with minepkg launch`,
	Aliases: []string{"isntall", "i", "add"},
	Run: func(cmd *cobra.Command, args []string) {
		instance, err := instances.DetectInstance()
		instance.MinepkgAPI = apiClient
		if err != nil {
			logger.Fail("Instance problem: " + err.Error())
		}
		fmt.Printf("Installing to %s\n", instance.Desc())
		fmt.Println() // empty line

		// create mod dir if not already present
		if err := os.MkdirAll(instance.ModsDirectory, 0755); err != nil {
			panic(err)
		}

		// no args: installing minepkg.toml dependencies
		if len(args) == 0 {
			installManifest(instance)
			return
		}

		firstArg := args[0]
		if strings.HasPrefix(firstArg, "https://") {
			switch {
			// got a minepkg url
			case strings.HasPrefix(firstArg, "https://minepkg.io/projects/"):
				projectname := firstArg[28:] // url minus first bits (just the name)
				err = installFromMinepkg([]string{projectname}, instance)
				if err != nil {
					logger.Fail(err.Error())
				}
				return
			}
			logger.Fail("Sorry. Don't know what to do with that url")
		}

		// fallback to minepkg
		err = installFromMinepkg(args, instance)
		if err != nil {
			logger.Fail(err.Error())
		}
	},
}
