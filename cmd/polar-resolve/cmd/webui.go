package cmd

import (
	"fmt"

	"github.com/Yeti47/polar-resolve/internal/web"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	webuiPort int
	webuiBind string
)

var webuiCmd = &cobra.Command{
	Use:   "web-ui",
	Short: "Start the web-based UI for upscaling",
	Long: `Start a local web server that provides a browser-based interface
for upscaling images and videos using Real-ESRGAN.`,
	RunE: runWebUI,
}

func init() {
	webuiCmd.Flags().IntVar(&webuiPort, "port", 8080, "Port to listen on")
	webuiCmd.Flags().StringVar(&webuiBind, "bind", "0.0.0.0", "Address to bind to")

	_ = viper.BindPFlag("port", webuiCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("bind", webuiCmd.Flags().Lookup("bind"))

	rootCmd.AddCommand(webuiCmd)
}

func runWebUI(cmd *cobra.Command, args []string) error {
	// Model download (if needed) happens in the background inside the server
	// so the web UI is available immediately and can show download progress.
	srv, err := web.NewServer(web.ServerConfig{
		Bind:      viper.GetString("bind"),
		Port:      viper.GetInt("port"),
		ModelPath: GetModelPath(),
		Device:    GetDevice(),
		LibPath:   GetLibPath(),
		Verbose:   IsVerbose(),
		Logger:    NewLogger(),
	})
	if err != nil {
		return fmt.Errorf("failed to start web server: %w", err)
	}

	return srv.Run()
}
