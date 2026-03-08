package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgDevice  string
	cfgModel   string
	cfgLibPath string
	cfgVerbose bool
)

var rootCmd = &cobra.Command{
	Use:   "polar-resolve",
	Short: "Upscale images and videos using Real-ESRGAN (ONNX)",
	Long: `polar-resolve is a CLI tool for upscaling images and videos 4×
using the Real-ESRGAN-General-x4v3 model via ONNX Runtime.
Supports CPU and AMD ROCm GPU acceleration.`,
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgDevice, "device", "auto", "Execution provider: auto, cpu, rocm")
	rootCmd.PersistentFlags().StringVar(&cfgModel, "model", "", "Path to a custom ONNX model file")
	rootCmd.PersistentFlags().StringVar(&cfgLibPath, "lib-path", "", "Path to libonnxruntime.so")
	rootCmd.PersistentFlags().BoolVar(&cfgVerbose, "verbose", false, "Enable verbose logging")

	_ = viper.BindPFlag("device", rootCmd.PersistentFlags().Lookup("device"))
	_ = viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	_ = viper.BindPFlag("lib-path", rootCmd.PersistentFlags().Lookup("lib-path"))
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
}

func initConfig() {
	viper.SetEnvPrefix("POLAR_RESOLVE")
	viper.AutomaticEnv()
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// GetDevice returns the configured execution provider.
func GetDevice() string {
	return viper.GetString("device")
}

// GetModelPath returns the configured model path (empty = auto-download).
func GetModelPath() string {
	return viper.GetString("model")
}

// GetLibPath returns the configured libonnxruntime.so path.
func GetLibPath() string {
	return viper.GetString("lib-path")
}

// IsVerbose returns whether verbose logging is enabled.
func IsVerbose() bool {
	return viper.GetBool("verbose")
}

// Logf prints a message if verbose logging is enabled.
func Logf(format string, args ...interface{}) {
	if IsVerbose() {
		fmt.Printf("[polar-resolve] "+format+"\n", args...)
	}
}

const (
	WorkspaceInput  = "/workspace/input"
	WorkspaceOutput = "/workspace/output"
)

// ResolveInputPath makes a relative path absolute under WorkspaceInput.
// Absolute paths are returned unchanged.
func ResolveInputPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(WorkspaceInput, path)
}

// ResolveOutputPath makes a relative path absolute under WorkspaceOutput.
// Absolute paths and empty strings are returned unchanged.
func ResolveOutputPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(WorkspaceOutput, path)
}
