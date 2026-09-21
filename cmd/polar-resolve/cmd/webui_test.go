package cmd

import (
	"testing"

	"github.com/spf13/viper"
)

func TestDisableVideoUpscalingConfig(t *testing.T) {
	t.Run("default enabled", func(t *testing.T) {
		t.Setenv("POLAR_RESOLVE_DISABLE_VIDEO_UPSCALING", "")
		initConfig()
		if viper.GetBool("disable-video-upscaling") {
			t.Error("expected video upscaling to be enabled by default")
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("POLAR_RESOLVE_DISABLE_VIDEO_UPSCALING", "true")
		initConfig()
		if !viper.GetBool("disable-video-upscaling") {
			t.Error("expected POLAR_RESOLVE_DISABLE_VIDEO_UPSCALING to disable video upscaling")
		}
	})
}
