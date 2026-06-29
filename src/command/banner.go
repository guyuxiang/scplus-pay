package command

import (
	"github.com/guyuxiang/scplus-pay/config"
	"github.com/gookit/color"
)

func printBanner() {
	color.Green.Printf("%s\n", "========== SC+Pay ==========")
	color.Infof(
		"scplus-pay version(%s) commit(%s) built(%s) Powered by %s %s \n",
		config.GetAppVersion(),
		config.GetBuildCommit(),
		config.GetBuildDate(),
		"guyuxiang",
		"https://github.com/guyuxiang/scplus-pay",
	)
}
