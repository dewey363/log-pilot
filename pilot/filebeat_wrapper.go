package pilot

import (
	"encoding/json"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/elastic/go-ucfg"
	"github.com/elastic/go-ucfg/yaml"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"regexp"
	"io/ioutil"
	"strings"
)

const (
	PILOT_FILEBEAT                = "filebeat"
	FILEBEAT_EXEC_BIN             = "/usr/bin/filebeat"
	FILEBEAT_REGISTRY_FILE 		  = "/var/lib/filebeat/registry"
	DOCKER_HOME_PATH              = "/var/lib/docker/"
	KUBELET_HOME_PATH             = "/var/lib/kubelet/"
	)

var filebeat *exec.Cmd

type FilebeatPiloter struct {
	name           string
	base           string
	confpath	   string
	watchDone      chan bool
	watchDuration  time.Duration
	watchContainer map[string]string
	//add flag for success start
	issuccess	   bool
}

func NewFilebeatPiloter(base string,confpath string) (Piloter, error) {
	return &FilebeatPiloter{
		name:           PILOT_FILEBEAT,
		base:           base,
		confpath:		confpath,
		watchDone:      make(chan bool),
		watchContainer: make(map[string]string, 0),
		watchDuration:  60 * time.Second,
		issuccess:      false,
	}, nil
}

var configOpts = []ucfg.Option{
	ucfg.PathSep("."),
	ucfg.ResolveEnv,
	ucfg.VarExp,
}

type Config struct {
	Paths []string `config:"paths"`
}

type FileInode struct {
	Inode  uint64 `json:"inode,"`
	Device uint64 `json:"device,"`
}

type RegistryState struct {
	Source      string        `json:"source"`
	Offset      int64         `json:"offset"`
	Timestamp   time.Time     `json:"timestamp"`
	TTL         time.Duration `json:"ttl"`
	Type        string        `json:"type"`
	FileStateOS FileInode
}

func (p *FilebeatPiloter) watch() error {
	log.Infof("%s watcher start", p.Name())
	for {
		select {
		case <-p.watchDone:
			log.Infof("%s watcher stop", p.Name())
			return nil
		case <-time.After(p.watchDuration):
			//log.Debugf("%s watcher scan", p.Name())
			err := p.scan()
			if err != nil {
				log.Errorf("%s watcher scan error: %v", p.Name(), err)
			}
		}
	}
	return nil
}

func (p *FilebeatPiloter) scan() error {
	states, err := p.getRegsitryState()
	if err != nil {
		return nil
	}

	configPaths := p.loadConfigPaths()
	for container := range p.watchContainer {
		confPath := p.ConfPathOf(container)
		if _, err := os.Stat(confPath); err != nil && os.IsNotExist(err) {
			log.Infof("log config %s.yml has been removed and ignore", container)
			delete(p.watchContainer, container)
		} else if p.canRemoveConf(container, states, configPaths) {
			log.Infof("try to remove log config %s.yml", container)
			if err := os.Remove(confPath); err != nil {
				log.Errorf("remove log config %s.yml fail: %v", container, err)
			} else {
				delete(p.watchContainer, container)
			}
		}
	}
	return nil
}

func (p *FilebeatPiloter) canRemoveConf(container string, registry map[string]RegistryState,
	configPaths map[string]string) bool {
	config, err := p.loadConfig(container)
	if err != nil {
		return false
	}

	for _, path := range config.Paths {
		autoMount := p.isAutoMountPath(filepath.Dir(path))
		logFiles, _ := filepath.Glob(path)
		for _, logFile := range logFiles {
			info, err := os.Stat(logFile)
			if err != nil && os.IsNotExist(err) {
				continue
			}
			if _, ok := registry[logFile]; !ok {
				log.Warnf("%s->%s registry not exist", container, logFile)
				continue
			}
			if registry[logFile].Offset < info.Size() {
				if autoMount { // ephemeral logs
					log.Infof("%s->%s does not finish to read", container, logFile)
					return false
				} else if _, ok := configPaths[path]; !ok { // host path bind
					log.Infof("%s->%s does not finish to read and not exist in other config",
						container, logFile)
					return false
				}
			}
		}
	}
	return true
}

func (p *FilebeatPiloter) loadConfig(container string) (*Config, error) {
	confPath := p.ConfPathOf(container)
	c, err := yaml.NewConfigWithFile(confPath, configOpts...)
	if err != nil {
		log.Errorf("read %s.yml log config error: %v", container, err)
		return nil, err
	}

	var config Config
	if err := c.Unpack(&config); err != nil {
		log.Errorf("parse %s.yml log config error: %v", container, err)
		return nil, err
	}
	return &config, nil
}

func (p *FilebeatPiloter) loadConfigPaths() map[string]string {
	paths := make(map[string]string, 0)
	confs, _ := ioutil.ReadDir(p.ConfHome())
	for _, conf := range confs {
		container := strings.TrimRight(conf.Name(), ".yml")
		if _, ok := p.watchContainer[container]; ok {
			continue // ignore removed container
		}

		config, err := p.loadConfig(container)
		if err != nil || config == nil {
			continue
		}

		for _, path := range config.Paths {
			if _, ok := paths[path]; !ok {
				paths[path] = container
			}
		}
	}
	return paths
}

func (p *FilebeatPiloter) isAutoMountPath(path string) bool {
	dockerVolumePattern := fmt.Sprintf("^%s.*$", filepath.Join(p.base, DOCKER_HOME_PATH))
	if ok, _ := regexp.MatchString(dockerVolumePattern, path); ok {
		return true
	}

	kubeletVolumePattern := fmt.Sprintf("^%s.*$", filepath.Join(p.base, KUBELET_HOME_PATH))
	ok, _ := regexp.MatchString(kubeletVolumePattern, path)
	return ok
}

func (p *FilebeatPiloter) getRegsitryState() (map[string]RegistryState, error) {
	f, err := os.Open(FILEBEAT_REGISTRY_FILE)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := json.NewDecoder(f)
	states := make([]RegistryState, 0)
	err = decoder.Decode(&states)
	if err != nil {
		return nil, err
	}

	statesMap := make(map[string]RegistryState, 0)
	for _, state := range states {
		if _, ok := statesMap[state.Source]; !ok {
			statesMap[state.Source] = state
		}
	}
	return statesMap, nil
}

func (p *FilebeatPiloter) feed(containerID string) error {
	if _, ok := p.watchContainer[containerID]; !ok {
		p.watchContainer[containerID] = containerID
		log.Infof("begin to watch log config: %s.yml", containerID)
	}
	return nil
}

func (p *FilebeatPiloter) Start() error {
	if p.issuccess {
		return fmt.Errorf(ERR_ALREADY_STARTED)
	}

	if filebeat != nil {
		return fmt.Errorf(ERR_ALREADY_STARTED)
	}

	log.Info("start filebeat")
	filebeat = exec.Command(FILEBEAT_EXEC_BIN, "-c", p.confpath + "/filebeat.yml")
	filebeat.Stderr = os.Stderr
	filebeat.Stdout = os.Stdout
	err := filebeat.Start()
	if err != nil {
		log.Error(err)
	}

	go func() {
		err := filebeat.Wait()
		if err != nil {
			log.Error(err)
		}else {
			p.issuccess = true
			log.Info("start success!")
		}

	}()

	go p.watch()
	return err
}

func (p *FilebeatPiloter) Stop() error {
	p.watchDone <- true
	return nil
}

func (p *FilebeatPiloter) Reload() error {
	log.Debug("not need to reload filebeat")
	return nil
}

func (p *FilebeatPiloter) ConfPathOf(container string) string {
	return fmt.Sprintf("%s/%s.yml", p.confpath+"/prospectors.d", container)
}

func (p *FilebeatPiloter) ConfHome() string {
	return p.confpath + "/prospectors.d"
}

func (p *FilebeatPiloter) Name() string {
	return p.name
}

func (p *FilebeatPiloter) OnDestroyEvent(container string) error {
	return p.feed(container)
}
