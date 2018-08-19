package pilot

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"golang.org/x/net/context"
)

/**
Label:
aliyun.log: /var/log/hello.log[:json][;/var/log/abc/def.log[:txt]]
*/

const (
	ENV_PILOT_LOG_PREFIX     		= "PILOT_LOG_PREFIX"
	ENV_PILOT_CREATE_SYMLINK        = "PILOT_CREATE_SYMLINK"
	LABEL_SERVICE_LOGS_TEMPL        = "%s.logs."
	ENV_SERVICE_LOGS_TEMPL          = "%s_logs_"
	SYMLINK_LOGS_BASE               = "/acs/log/"
	LABEL_PROJECT                   = "com.docker.compose.project"
	LABEL_PROJECT_SWARM_MODE        = "com.docker.stack.namespace"
	LABEL_SERVICE                   = "com.docker.compose.service"
	LABEL_SERVICE_SWARM_MODE        = "com.docker.swarm.service.name"
	LABEL_POD                       = "io.kubernetes.pod.name"
	LABEL_K8S_POD_NAMESPACE         = "io.kubernetes.pod.namespace"
	LABEL_K8S_CONTAINER_NAME        = "io.kubernetes.container.name"
	ERR_ALREADY_STARTED 			= "already started"
)

var NODE_NAME = os.Getenv("NODE_NAME")

type Pilot struct {
	mutex         sync.Mutex
	tpl           *template.Template
	tplGlobal     *template.Template
	//add for config global
	tplGlobalFlag bool
	base          string
	dockerClient  *client.Client
	reloadChan    chan bool
	lastReload    time.Time
	piloter       Piloter
	logPrefix     []string
	createSymlink bool
	//add
	lid           int
	//add for filebeat config
	confpath	  string
	logpath		  string
	datapath      string
	loglevel      string
	reloadtime    string
}

type Piloter interface {
	Name() string
	Start() error
	Reload() error
	Stop() error
	ConfHome() string
	ConfPathOf(container string) string
	OnDestroyEvent(container string) error
}

//add for filebeat config
type Filebeatconfig struct {
	Confpath 	string
	Logpath  	string
	Datapath 	string
	Loglevel 	string
	Reloadtime  string
}

func Run(tpl string, tplGlobal string, baseDir string, filebeat *Filebeatconfig) error {
	p, err := New(tpl,tplGlobal,baseDir,filebeat)
	if err != nil {
		panic(err)
	}
	return p.watch()
}

func New(tplStr string, tplStrGlobal string, baseDir string, filebeat *Filebeatconfig) (*Pilot, error) {
	tpl, err := template.New("pilot").Parse(tplStr)
	if err != nil {
		return nil, err
	}

	tplGlobal,err:=template.New("global").Parse(tplStrGlobal)
	if err != nil {
		return nil, err
	}

	if os.Getenv("DOCKER_API_VERSION") == "" {
		os.Setenv("DOCKER_API_VERSION", "1.23")
	}

	dockerclient, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	//we just use filebeat
	//piloter, _ := NewFluentdPiloter()
	//if os.Getenv(ENV_PILOT_TYPE) == PILOT_FILEBEAT {
	//	piloter, _ = NewFilebeatPiloter(baseDir)
	//}

	piloter, _ := NewFilebeatPiloter(baseDir,filebeat.Confpath)

	//logPrefix := []string{"aliyun"}
	//default prefix is sn
	logPrefix := []string{"sn"}
	if os.Getenv(ENV_PILOT_LOG_PREFIX) != "" {
		envLogPrefix := os.Getenv(ENV_PILOT_LOG_PREFIX)
		logPrefix = strings.Split(envLogPrefix, ",")
	}

	createSymlink := os.Getenv(ENV_PILOT_CREATE_SYMLINK) == "true"
	return &Pilot{
		dockerClient:  dockerclient,
		tpl:           tpl,
		tplGlobal:     tplGlobal,
		tplGlobalFlag: true,
		base:          baseDir,
		reloadChan:    make(chan bool),
		piloter:       piloter,
		logPrefix:     logPrefix,
		createSymlink: createSymlink,
		lid:		   0,
		confpath:	   filebeat.Confpath,
		logpath:	   filebeat.Logpath,
		datapath:      filebeat.Datapath,
		loglevel:      filebeat.Loglevel,
		reloadtime:    filebeat.Reloadtime,
	}, nil
}

func (p *Pilot) watch() error {
	if err := p.processAllContainers(); err != nil {
		return err
	}

	//just start for global config create
	//err := p.piloter.Start()
	//if err != nil && ERR_ALREADY_STARTED != err.Error() {
	//	return err
	//}

	p.lastReload = time.Now()
	go p.doReload()

	ctx := context.Background()
	filter := filters.NewArgs()
	filter.Add("type", "container")

	options := types.EventsOptions{
		Filters: filter,
	}
	msgs, errs := p.client().Events(ctx, options)
	for {
		select {
		case msg := <-msgs:
			if err := p.processEvent(msg); err != nil {
				log.Errorf("fail to process event: %v,  %v", msg, err)
			}
		case err := <-errs:
			log.Warnf("error: %v", err)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			} else {
				msgs, errs = p.client().Events(ctx, options)
			}
		}
	}
}

type LogConfig struct {
	Name         string
	HostDir      string
	ContainerDir string
	Format       string
	FormatConfig map[string]string
	File         string
	Tags         map[string]string
	//modify targrt to topic
	Target       string
	EstimateTime bool
	Stdout       bool
	//add for kafka
	Brokerlist	 string
	Merge		 string
	Split		 string
	Prefix 		 string
	//add for format
	Version      string
	Ldc          string
	Hostgroup    string
	Appid        string
	Ip           string
	Path         string
	Lid          int
	//add for path
	Confpath	 string
	Logpath 	 string
	Datapath	 string
	Loglevel     string
	Reloadtime   string

}

func (p *Pilot) cleanConfigs() error {
	confDir := fmt.Sprintf(p.piloter.ConfHome())
	d, err := os.Open(confDir)
	if err != nil {
		return err
	}
	defer d.Close()

	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, name := range names {
		configpath := filepath.Join(confDir, name)
		stat, err := os.Stat(filepath.Join(confDir, name))
		if err != nil {
			return err
		}
		if stat.Mode().IsRegular() {
			if err := os.Remove(configpath); err != nil {
				return err
			}
		}
	}

	//clean global config
	globalconfig := filepath.Join(p.confpath,"filebeat.yml")
	gStat, err := os.Stat(filepath.Join(globalconfig))
	if err != nil {
		if os.IsNotExist(err){
			log.Debug("global config filebeat.yml is not exist!!")
			return nil
		}
		return err
	}
	if gStat.Mode().IsRegular() {
		if err := os.Remove(globalconfig); err != nil {
			return err
		}
	}

	return nil
}

func (p *Pilot) processAllContainers() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	opts := types.ContainerListOptions{}
	containers, err := p.client().ContainerList(context.Background(), opts)
	if err != nil {
		return err
	}

	//clean config
	if err := p.cleanConfigs(); err != nil {
		return err
	}

	containerIDs := make(map[string]string, 0)
	for _, c := range containers {
		i := 1
		log.Debugf("begin to deal container one by one current deal %d container is %s",i,c.ID)
		if _, ok := containerIDs[c.ID]; !ok {
			containerIDs[c.ID] = c.ID
		}
		if c.State == "removing" {
			continue
		}
		containerJSON, err := p.client().ContainerInspect(context.Background(), c.ID)
		if err != nil {
			return err
		}

		if err = p.newContainer(&containerJSON); err != nil {
			log.Errorf("fail to process container %s: %v", containerJSON.Name, err)
		}
		i++
	}
	return p.processAllVolumeSymlink(containerIDs)
}

func (p *Pilot) processAllVolumeSymlink(existingContainerIDs map[string]string) error {
	symlinkContainerIDs := p.listAllSymlinkContainer()
	for containerID := range symlinkContainerIDs {
		if _, ok := existingContainerIDs[containerID]; !ok {
			p.removeVolumeSymlink(containerID)
		}
	}
	return nil
}

func (p *Pilot) listAllSymlinkContainer() map[string]string {
	containerIDs := make(map[string]string, 0)
	linkBaseDir := path.Join(p.base, SYMLINK_LOGS_BASE)
	if _, err := os.Stat(linkBaseDir); err != nil && os.IsNotExist(err) {
		return containerIDs
	}

	projects := listSubDirectory(linkBaseDir)
	for _, project := range projects {
		projectPath := path.Join(linkBaseDir, project)
		services := listSubDirectory(projectPath)
		for _, service := range services {
			servicePath := path.Join(projectPath, service)
			containers := listSubDirectory(servicePath)
			for _, containerID := range containers {
				if _, ok := containerIDs[containerID]; !ok {
					containerIDs[containerID] = containerID
				}
			}
		}
	}
	return containerIDs
}

func listSubDirectory(path string) []string {
	subdirs := make([]string, 0)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return subdirs
	}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Warnf("read %s error: %v", path, err)
		return subdirs
	}

	for _, file := range files {
		if file.IsDir() {
			subdirs = append(subdirs, file.Name())
		}
	}
	return subdirs
}

func putIfNotEmpty(store map[string]string, key, value string) {
	if key == "" || value == "" {
		return
	}
	store[key] = value
}

func put(store map[string]string, key, value string) {
	if value == "" {
		store[key] = "-"
	}else{
		store[key] = value
	}
}

func container(containerJSON *types.ContainerJSON) map[string]string {
	labels := containerJSON.Config.Labels
	c := make(map[string]string)
	putIfNotEmpty(c, "docker_app", labels[LABEL_PROJECT])
	putIfNotEmpty(c, "docker_app", labels[LABEL_PROJECT_SWARM_MODE])
	putIfNotEmpty(c, "docker_service", labels[LABEL_SERVICE])
	putIfNotEmpty(c, "docker_service", labels[LABEL_SERVICE_SWARM_MODE])
	putIfNotEmpty(c, "k8s_pod", labels[LABEL_POD])
	putIfNotEmpty(c, "k8s_pod_namespace", labels[LABEL_K8S_POD_NAMESPACE])
	putIfNotEmpty(c, "k8s_container_name", labels[LABEL_K8S_CONTAINER_NAME])
	putIfNotEmpty(c, "k8s_node_name", NODE_NAME)
	putIfNotEmpty(c, "docker_container", strings.TrimPrefix(containerJSON.Name, "/"))
	put(c, "version",containerJSON.Config.Labels["version"])
	put(c, "ldc",containerJSON.Config.Labels["ldc"])
	put(c, "hostgroup",containerJSON.Config.Labels["hostgroup"])
	put(c, "appid",containerJSON.Config.Labels["appid"])
	put(c, "ip",containerJSON.Config.Labels["KUBERNETES.POD.IP"])
	extension(c, containerJSON)
	return c
}

func (p *Pilot) newContainer(containerJSON *types.ContainerJSON) error {
	id := containerJSON.ID
	jsonLogPath := containerJSON.LogPath
	mounts := containerJSON.Mounts
	labels := containerJSON.Config.Labels
	env := containerJSON.Config.Env
	log.Debugf("current deal cintainer info: \n id : %s ,\n log path : %s ,\n mounts : %s ,\n labels : %s ,\n env : %s \n",id,jsonLogPath,mounts,labels,env)

	for _, e := range env {
		for _, prefix := range p.logPrefix {
			serviceLogs := fmt.Sprintf(ENV_SERVICE_LOGS_TEMPL, prefix)
			log.Debugf("current container button prefix: %s",serviceLogs)
			log.Debugf("current container env: %s",e)

			if !strings.HasPrefix(e, serviceLogs) &&
				!strings.HasPrefix(e, "version") &&
				!strings.HasPrefix(e, "ldc") &&
				!strings.HasPrefix(e, "hostgroup") &&
				!strings.HasPrefix(e, "appid") &&
				!strings.HasPrefix(e, "KUBERNETES_POD_IP"){
				continue
			}

			envLabel := strings.SplitN(e, "=", 2)
			if len(envLabel) == 2 {
				labelKey := strings.Replace(envLabel[0], "_", ".", -1)
				labels[labelKey] = envLabel[1]
				log.Debugf("current container env insert into labels map: %s:%s",labelKey,labels[labelKey])

			}
		}
	}

	containerJSON.Config.Labels = labels
	container := container(containerJSON)

	logConfigs, err := p.getLogConfigs(jsonLogPath, mounts, labels)
	log.Debugf("current container all of label and env with on prefix info: %s",logConfigs)
	if err != nil {
		return err
	}

	if len(logConfigs) == 0 {
		log.Debugf("%s has not log config, skip", id)
		return nil
	}

	// create symlink
	p.createVolumeSymlink(containerJSON)

	//pilot.findMounts(logConfigs, jsonLogPath, mounts)
	//生成配置
	logConfig, logConfigGlobal, err := p.render(id, container, logConfigs)
	log.Debugf("the content of config for global is %s",logConfigGlobal)
	log.Debugf("the content of config is %s",logConfig)
	if err != nil {
		return err
	}

	//TODO validate config before save
	//log.Debugf("container %s log config: %s", id, logConfig)
	if err = ioutil.WriteFile(p.piloter.ConfPathOf(id), []byte(logConfig), os.FileMode(0644)); err != nil {
		return err
	}

	if p.tplGlobalFlag {
		globalConfig := fmt.Sprintf("%s/%s.yml", p.confpath, PILOT_FILEBEAT)
		log.Debugf("begin to create global config just for the first time,config name is %s",globalConfig)
		if err = ioutil.WriteFile(globalConfig, []byte(logConfigGlobal), os.FileMode(0644)); err != nil {
			return err
		}
		p.tplGlobalFlag = false

		//start filebeat
		err := p.piloter.Start()
		if err != nil && ERR_ALREADY_STARTED != err.Error() {
			return err
		}
	}

	p.tryReload()
	return nil
}

func (p *Pilot) tryReload() {
	select {
	case p.reloadChan <- true:
	default:
		log.Info("Another load is pending")
	}
}

func (p *Pilot) doReload() {
	log.Info("Reload gorouting is ready")
	for {
		<-p.reloadChan
		p.reload()
	}
}

func (p *Pilot) delContainer(id string) error {
	p.removeVolumeSymlink(id)

	// refactor in the future
	if p.piloter.Name() == PILOT_FLUENTD {
		clean := func() {
			log.Infof("Try removing log config %s", id)
			if err := os.Remove(p.piloter.ConfPathOf(id)); err != nil {
				log.Warnf("removing %s log config failure", id)
				return
			}
			p.tryReload()
		}
		time.AfterFunc(15*time.Minute, clean)
		return nil
	} else {
		return p.piloter.OnDestroyEvent(id)
	}
}

func (p *Pilot) client() *client.Client {
	return p.dockerClient
}

func (p *Pilot) processEvent(msg events.Message) error {
	containerId := msg.Actor.ID
	ctx := context.Background()
	switch msg.Action {
	case "start", "restart":
		log.Debugf("Process container start event: %s", containerId)
		if p.exists(containerId) {
			log.Debugf("%s is already exists.", containerId)
			return nil
		}
		containerJSON, err := p.client().ContainerInspect(ctx, containerId)
		if err != nil {
			return err
		}
		return p.newContainer(&containerJSON)
	case "destroy":
		log.Debugf("Process container destory event: %s", containerId)
		err := p.delContainer(containerId)
		if err != nil {
			log.Warnf("Process container destory event error: %s, %s", containerId, err.Error())
		}
	}
	return nil
}

func (p *Pilot) hostDirOf(path string, mounts map[string]types.MountPoint) string {
	confPath := path
	for {
		if point, ok := mounts[path]; ok {
			if confPath == path {
				return point.Source
			} else {
				relPath, err := filepath.Rel(path, confPath)
				if err != nil {
					panic(err)
				}
				return fmt.Sprintf("%s/%s", point.Source, relPath)
			}
		}
		path = filepath.Dir(path)
		if path == "/" || path == "." {
			break
		}
	}
	return ""
}

func (p *Pilot) parseTags(tags string) (map[string]string, error) {
	tagMap := make(map[string]string)
	if tags == "" {
		return tagMap, nil
	}

	kvArray := strings.Split(tags, ",")
	for _, kv := range kvArray {
		arr := strings.Split(kv, "=")
		if len(arr) != 2 {
			return nil, fmt.Errorf("%s is not a valid k=v format", kv)
		}
		key := strings.TrimSpace(arr[0])
		value := strings.TrimSpace(arr[1])
		if key == "" || value == "" {
			return nil, fmt.Errorf("%s is not a valid k=v format", kv)
		}
		tagMap[key] = value
	}
	return tagMap, nil
}

//add for physics machine log collect
func (p *Pilot)getPhysicsLogConfig(name string, info *LogInfoNode, jsonLogPath string, mounts map[string]types.MountPoint) (*LogConfig, error) {
	logpath := strings.TrimSpace(info.value)
	if logpath == "" {
		return nil, fmt.Errorf("logpath for %s is empty", name)
	}

	var  brokerlist string
	brokerlistTmp := info.get("brokerlist")
	if brokerlistTmp == "" {
		return nil, fmt.Errorf("%s","brokerlist can not be empty!!")
	}
	brokerlists := strings.Split(brokerlistTmp,",")
	length := len(brokerlists)


	for k,v := range brokerlists{
		brokerlist += "\"" + v + "\""
		if k != length-1 {
			brokerlist += ","
		}
	}

	merge := info.get("merge")

	prefix := info.get("prefix")

	split := info.get("split")
	if split == "\t" || split == ""{
		split = "	"
	}

	p.lid++
	if p.lid == 10000 {
		p.lid = 0
	}

	cfg := &LogConfig{
		File:         "",
		HostDir:      "",
		Brokerlist:	  brokerlist,
		Merge:		  merge,
		Prefix:       prefix,
		Split:		  split,
		Path:         logpath,
		Lid:          p.lid,
		Confpath:	  p.confpath,
		Logpath:	  p.logpath,
		Datapath:	  p.datapath,
		Loglevel:     p.loglevel,
		Reloadtime:	  p.reloadtime,
	}
	return cfg,nil
}


func (p *Pilot) parseLogConfig(name string, info *LogInfoNode, jsonLogPath string, mounts map[string]types.MountPoint) (*LogConfig, error) {
	logpath := strings.TrimSpace(info.value)
	if logpath == "" {
		return nil, fmt.Errorf("logpath for %s is empty", name)
	}

	tags := info.get("tags")
	tagMap, err := p.parseTags(tags)
	if err != nil {
		return nil, fmt.Errorf("parse tags for %s error: %v", name, err)
	}

	target := info.get("topic")

	// add default index or topic
	if _, ok := tagMap["index"]; !ok {
		if target != "" {
			tagMap["index"] = target
		} else {
			tagMap["index"] = name
		}
	}

	if _, ok := tagMap["topic"]; !ok {
		if target != "" {
			tagMap["topic"] = target
		} else {
			tagMap["topic"] = name
		}
	}

	format := info.children["format"]
	if format == nil || format.value == "none" {
		format = newLogInfoNode("nonex")
	}

	formatConfig, err := Convert(format)
	if err != nil {
		return nil, fmt.Errorf("in log %s: format error: %v", name, err)
	}

	//特殊处理regex
	if format.value == "regexp" {
		format.value = fmt.Sprintf("/%s/", formatConfig["pattern"])
		delete(formatConfig, "pattern")
	}

	var  brokerlist string
	brokerlistTmp := info.get("brokerlist")
	if brokerlistTmp == "" {
		return nil, fmt.Errorf("%s","brokerlist can not be empty!!")
	}
	brokerlists := strings.Split(brokerlistTmp,",")
	length := len(brokerlists)


	for k,v := range brokerlists{
		brokerlist += "\"" + v + "\""
		if k != length-1 {
			brokerlist += ","
		}
	}

	merge := info.get("merge")

	prefix := info.get("prefix")

	split := info.get("split")
	if split == "\t" || split == ""{
		split = "	"
	}

	p.lid++
	if p.lid == 10000 {
		p.lid = 0
	}

	if logpath == "stdout" {
		logFile := filepath.Base(jsonLogPath)
		if p.piloter.Name() == PILOT_FILEBEAT {
			logFile = logFile + "*"
		}

		return &LogConfig{
			Name:         name,
			HostDir:      filepath.Join(p.base, filepath.Dir(jsonLogPath)),
			File:         logFile,
			Format:       format.value,
			Tags:         tagMap,
			FormatConfig: map[string]string{"time_format": "%Y-%m-%dT%H:%M:%S.%NZ"},
			Target:       target,
			EstimateTime: false,
			Stdout:       true,
			Merge:		  merge,
			Prefix:       prefix,
		}, nil
	}

	//logConfig.containerDir match types.mountPoint
	/**
	  场景：
	  1. 容器一个路径，中间有多级目录对应宿主机不同的目录
	  2. containerdir对应的目录不是直接挂载的，挂载的是它上级的目录

	  查找：从containerdir开始查找最近的一层挂载
	*/

	if !filepath.IsAbs(logpath) {
		return nil, fmt.Errorf("%s must be absolute path, for %s", logpath, name)
	}
	containerDir := filepath.Dir(logpath)
	file := filepath.Base(logpath)
	if file == "" {
		return nil, fmt.Errorf("%s must be a file path, not directory, for %s", logpath, name)
	}

	hostDir := p.hostDirOf(containerDir, mounts)
	if hostDir == "" {
		return nil, fmt.Errorf("in log %s: %s is not mount on host", name, logpath)
	}

	cfg := &LogConfig{
		Name:         name,
		ContainerDir: containerDir,
		Format:       format.value,
		File:         file,
		Tags:         tagMap,
		HostDir:      filepath.Join(p.base, hostDir),
		FormatConfig: formatConfig,
		Target:       target,
		Brokerlist:	  brokerlist,
		Merge:		  merge,
		Prefix:       prefix,
		Split:		  split,
		Path:         logpath,
		Lid:          p.lid,
		Confpath:	  p.confpath,
		Logpath:	  p.logpath,
		Datapath:	  p.datapath,
		Loglevel:     p.loglevel,
		Reloadtime:	  p.reloadtime,
	}
	if formatConfig["time_key"] == "" {
		cfg.EstimateTime = true
		cfg.FormatConfig["time_key"] = "_timestamp"
	}
	return cfg, nil
}

type LogInfoNode struct {
	value    string
	children map[string]*LogInfoNode
}

func newLogInfoNode(value string) *LogInfoNode {
	return &LogInfoNode{
		value:    value,
		children: make(map[string]*LogInfoNode),
	}
}

func (node *LogInfoNode) insert(keys []string, value string) error {
	if len(keys) == 0 {
		return nil
	}
	key := keys[0]
	if len(keys) > 1 {
		if child, ok := node.children[key]; ok {
			child.insert(keys[1:], value)
		} else {
			return fmt.Errorf("%s has no parent node", key)
		}
	} else {
		child := newLogInfoNode(value)
		node.children[key] = child
	}
	return nil
}

func (node *LogInfoNode) get(key string) string {
	if child, ok := node.children[key]; ok {
		return child.value
	}
	return ""
}

func (p *Pilot) getLogConfigs(jsonLogPath string, mounts []types.MountPoint, labels map[string]string) ([]*LogConfig, error) {
	var ret []*LogConfig

	mountsMap := make(map[string]types.MountPoint)
	for _, nmount := range mounts {
		mountsMap[nmount.Destination] = nmount
	}

	var labelNames []string
	//sort keys
	for k:= range labels {
		labelNames = append(labelNames, k)
	}

	sort.Strings(labelNames)
	root := newLogInfoNode("")
	for _, k := range labelNames {
		for _, prefix := range p.logPrefix {
			serviceLogs := fmt.Sprintf(LABEL_SERVICE_LOGS_TEMPL, prefix)
			if !strings.HasPrefix(k, serviceLogs) || strings.Count(k, ".") == 1 {
				continue
			}

			logLabel := strings.TrimPrefix(k, serviceLogs)
			if err := root.insert(strings.Split(logLabel, "."), labels[k]); err != nil {
				return nil, err
			}
		}
	}

	for name, node := range root.children {
		logConfig, err := p.parseLogConfig(name, node, jsonLogPath, mountsMap)
		if err != nil {
			return nil, err
		}
		ret = append(ret, logConfig)
	}
	return ret, nil
}

func (p *Pilot) exists(containId string) bool {
	if _, err := os.Stat(p.piloter.ConfPathOf(containId)); os.IsNotExist(err) {
		return false
	}
	return true
}

func (p *Pilot) render(containerId string, container map[string]string, configList []*LogConfig) (string, string, error) {
	for _, config := range configList {
		log.Infof("container need to create config info: %s = %v", containerId, config)
	}

	//output := os.Getenv(ENV_FLUENTD_OUTPUT)
	//if p.piloter.Name() == PILOT_FILEBEAT {
	//	output = os.Getenv(ENV_FILEBEAT_OUTPUT)
	//}

	//just use filebeat and output for kafka
	output := "kafka"

	var buf bytes.Buffer
	contexts := map[string]interface{}{
		"containerId": containerId,
		"configList":  configList,
		"container":   container,
		"output":      output,
	}

	if err := p.tpl.Execute(&buf, contexts); err != nil {
		return "","", err
	}
	//log.Debugf("need to write to config file buf: %s",buf)

	//create config file for global
	var bufGlobal bytes.Buffer
	if p.tplGlobalFlag {
		if err := p.tplGlobal.Execute(&bufGlobal, contexts); err != nil {
			return buf.String(),"", err
		}
		//log.Debugf("need to write to global config file buf: %s",bufGlobal)
	}

	return buf.String(), bufGlobal.String(), nil
}

func (p *Pilot) reload() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	log.Infof("Reload %s", p.piloter.Name())
	interval := time.Now().Sub(p.lastReload)
	time.Sleep(30*time.Second - interval)
	log.Info("Start reloading")
	err := p.piloter.Reload()
	p.lastReload = time.Now()
	return err
}

func (p *Pilot) createVolumeSymlink(containerJSON *types.ContainerJSON) error {
	if !p.createSymlink {
		return nil
	}

	linkBaseDir := path.Join(p.base, SYMLINK_LOGS_BASE)
	if _, err := os.Stat(linkBaseDir); err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(linkBaseDir, 0777); err != nil {
			log.Errorf("create %s error: %v", linkBaseDir, err)
		}
	}

	applicationInfo := container(containerJSON)
	containerLinkBaseDir := path.Join(linkBaseDir, applicationInfo["docker_app"],
		applicationInfo["docker_service"], containerJSON.ID)
	symlinks := make(map[string]string, 0)
	for _, mountPoint := range containerJSON.Mounts {
		if mountPoint.Type != mount.TypeVolume {
			continue
		}

		volume, err := p.client().VolumeInspect(context.Background(), mountPoint.Name)
		if err != nil {
			log.Errorf("inspect volume %s error: %v", mountPoint.Name, err)
			continue
		}

		symlink := path.Join(containerLinkBaseDir, volume.Name)
		if _, ok := symlinks[volume.Mountpoint]; !ok {
			symlinks[volume.Mountpoint] = symlink
		}
	}

	if len(symlinks) == 0 {
		return nil
	}

	if _, err := os.Stat(containerLinkBaseDir); err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(containerLinkBaseDir, 0777); err != nil {
			log.Errorf("create %s error: %v", containerLinkBaseDir, err)
			return err
		}
	}

	for mountPoint, symlink := range symlinks {
		err := os.Symlink(mountPoint, symlink)
		if err != nil && !os.IsExist(err) {
			log.Errorf("create symlink %s error: %v", symlink, err)
		}
	}
	return nil
}

func (p *Pilot) removeVolumeSymlink(containerId string) error {
	if !p.createSymlink {
		return nil
	}

	linkBaseDir := path.Join(p.base, SYMLINK_LOGS_BASE)
	containerLinkDirs, _ := filepath.Glob(path.Join(linkBaseDir, "*", "*", containerId))
	if containerLinkDirs == nil {
		return nil
	}
	for _, containerLinkDir := range containerLinkDirs {
		if err := os.RemoveAll(containerLinkDir); err != nil {
			log.Warnf("remove error: %v", err)
		}
	}
	return nil
}

