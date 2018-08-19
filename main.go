package main

import (
	"flag"
	"github.com/AliyunContainerService/log-pilot/pilot"
	log "github.com/Sirupsen/logrus"
	"io/ioutil"
	"os"
	"path/filepath"
)

func main() {
	template := flag.String("template", "", "Template for filebeat config.")
	global := flag.String("global-template", "", "Template for filebeat Global config.")
	base := flag.String("base", "/", "Directory which mount host root.")
	level := flag.String("log-level", "INFO", "log-pilot Log level")
	filebeatconfpath := flag.String("filebeat-config-path", "/opt/log/filebeat", "filebeat config path")
	filebeatlogpath := flag.String("filebeat-log-path", "/opt/log/filebeat/log", "filebeat log path")
	filebeatdatapath := flag.String("filebeat-data-path", "/opt/log/filebeat/data", "filebeat data home path")
	filebeatloglevel := flag.String("filebeat-log-level", "INFO", "filebeat Log level")
	filebeatreloadtime := flag.String("filebeat-reload-time", "10s", "filebeat reload time")

	flag.Parse()

	log.SetOutput(os.Stdout)
	logLevel, err := log.ParseLevel(*level)
	if err != nil {
		panic(err)
	}
	log.SetLevel(logLevel)

	baseDir := abspath(base)
	if baseDir == "/" {
		baseDir = ""
	}

	if *template == "" || *global == "" {
		panic("template or global template file can not be emtpy")
	}

	filebeattem := readfile(template)
	filebeatglobaltem := readfile(global)

	filebeat := &pilot.Filebeatconfig{
		Confpath:   abspath(filebeatconfpath),
		Logpath:    abspath(filebeatlogpath),
		Datapath:   abspath(filebeatdatapath),
		Loglevel:   *filebeatloglevel,
		Reloadtime: *filebeatreloadtime,
	}

	log.Fatal(pilot.Run(filebeattem, filebeatglobaltem, baseDir, filebeat))
}

func abspath(req *string) string {
	res, err := filepath.Abs(*req)
	if err != nil {
		panic(err)
	}
	return res
}

func readfile(req *string) string {
	res, err := ioutil.ReadFile(*req)
	if err != nil {
		panic(err)
	}
	return string(res)
}
