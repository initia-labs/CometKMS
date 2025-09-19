package cmd

import "github.com/spf13/cobra"

var (
	rootCmd  = &cobra.Command{Use: "cmkms", Short: "CMKMS signer and lease coordinator"}
	homeDir  string
	confPath string
	fileCfg  configFile
)

func init() {
	rootCmd.PersistentFlags().StringVar(&homeDir, "home", defaultHomeDir(), "CMKMS home directory")
	cobra.OnInitialize(func() { ensureConfig(&homeDir) })
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// HomeDir returns the configured cmkms home directory.
func HomeDir() string {
	return homeDir
}

// ConfigFile returns the path to the active configuration file.
func ConfigFile() string {
	return confPath
}

// Config returns the parsed configuration values.
func Config() configFile {
	return fileCfg
}
