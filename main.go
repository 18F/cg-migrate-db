package main

import (
	"bytes"
	"code.cloudfoundry.org/cli/cf/configuration/confighelpers"
	"code.cloudfoundry.org/cli/cf/models"
	"code.cloudfoundry.org/cli/plugin"
	"code.cloudfoundry.org/cli/plugin/models"
	"encoding/json"
	"fmt"
	"github.com/dchest/uniuri"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	plugin.Start(newExportPlugin())
}

func newConfig() Config {
	return Config{Version: 1}
}

type Config struct {
	Version int           `json:"version"`
	Entries []ConfigEntry `json:"entries"`
}

type ConfigEntry struct {
	AppName           string      `json:"app_name"`
	AppGUID           string      `json:"app_guid"`
	Space             string      `json:"space"`
	SpaceGUID         string      `json:"space_guid"`
	Org               string      `json:"org"`
	OrgGUID           string      `json:"org_guid"`
	API               string      `json:"api"`
	APIVersion        string      `json:"api_version"`
	SourceServiceName string      `json:"source_service_name"`
	SourceServiceGUID string      `json:"source_service_guid"`
	SourceServiceType string      `json:"source_service_type"`
	SourceServicePlan string      `json:"source_service_plan"`
	StoreServiceType  string      `json:"store_service_type"`
	StoreServiceGUID  string      `json:"store_service_guid"`
	StoreServiceName  string      `json:"store_service_name"`
	Credentials       interface{} `json:"credentials"`
}

func newExportPlugin() *ExportPlugin {
	configPath, err := confighelpers.DefaultFilePath()
	if err != nil {
		panic(err)
	}
	pluginPath := filepath.Join(filepath.Dir(configPath), "export-data-plugin")
	os.Mkdir(pluginPath, 0700)
	var config Config
	pluginConfigPath := filepath.Join(pluginPath, "export-data.json")
	pluginConfigData, err := ioutil.ReadFile(pluginConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = newConfig()
		} else {
			fmt.Println(err)
			os.Exit(1)
		}
	} else {
		err := json.Unmarshal(pluginConfigData, &config)
		if err != nil {
			fmt.Printf("Unable to read config at %s. Exiting...\n", pluginConfigPath)
		}
	}
	return &ExportPlugin{
		pluginPath: pluginPath,
		config:     config,
		configPath: pluginConfigPath,
	}
}

type ExportPlugin struct {
	pluginPath string
	config     Config
	configPath string
}

func (p *ExportPlugin) GetMetadata() plugin.PluginMetadata {
	return plugin.PluginMetadata{
		Name: "cg-export-db",
		Version: plugin.VersionType{
			Major: 0,
			Minor: 0,
			Build: 2,
		},
		Commands: []plugin.Command{
			{
				Name:     "import-data",
				HelpText: "Imports data from s3 bucket to a destination",
			},
			{
				Name:     "export-data",
				HelpText: "Exports data to s3 bucket from a source",
			},
			{
				Name:     "upload-backup-data",
				HelpText: "Uploads local data into a s3 bucket",
				UsageDetails: plugin.Usage{
					Usage: "cf upload-backup-data [filepath]",
				},
			},
			{
				Name:     "download-backup-data",
				HelpText: "Downloads data from s3 bucket to your local file system",
				UsageDetails: plugin.Usage{
					Usage: "cf download-backup-data",
				},
			},
			{
				Name:     "clean-export-config",
				HelpText: "Cleans config data",
			},
		},
	}
}

func (p *ExportPlugin) WriteConfigOrExit() {
	configData, err := json.Marshal(p.config)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	err = ioutil.WriteFile(p.configPath, configData, 0644)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func (p *ExportPlugin) Run(cliConnection plugin.CliConnection, args []string) {
	var err error
	writeConfig := false
	if args[0] == "export-data" {
		err = p.exportData(cliConnection)
		writeConfig = true
	} else if args[0] == "import-data" {
		err = p.importData(cliConnection)
	} else if args[0] == "clean-export-config" {
		p.config = newConfig()
		writeConfig = true
	} else if args[0] == "upload-backup-data" {
		err = p.uploadBackupData(cliConnection, args)
	} else if args[0] == "download-backup-data" {
		err = p.downloadBackupData(cliConnection)
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if writeConfig {
		p.WriteConfigOrExit()
	}
	os.Exit(0)
}

func (p *ExportPlugin) downloadBackupData(cliConnection plugin.CliConnection) error {
	if len(p.config.Entries) < 1 {
		return fmt.Errorf("Please run export-data in order for the plugin to get the credentials to a s3 bucket")
	}
	configEntry, err := p.promptImportSelection("Input the number for the service you want to use to upload the backup\n")
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(configEntry.Credentials)
	var s3Creds S3Creds
	err = json.Unmarshal(raw, &s3Creds)
	if err != nil {
		return fmt.Errorf("Unable to convert credentials into s3 credentials")
	}
	err = VerifyValidS3Creds(s3Creds)
	if err != nil {
		return err
	}
	err = downloadFile(s3Creds)
	if err != nil {
		return err
	}
	return nil
}

func (p *ExportPlugin) uploadBackupData(cliConnection plugin.CliConnection, args []string) error {
	if len(p.config.Entries) < 1 {
		return fmt.Errorf("Please run export-data in order for the plugin to get the credentials to a s3 bucket")
	}

	if len(args) != 2 {
		return fmt.Errorf("Not enough arguments for command. Check usage...")
	}
	path := args[1]
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Unable to access file %s. Error: %s", file.Name(), err.Error())
	}
	defer file.Close()
	configEntry, err := p.promptImportSelection("Input the number for the service you want to use to upload the backup\n")
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(configEntry.Credentials)
	var s3Creds S3Creds
	err = json.Unmarshal(raw, &s3Creds)
	if err != nil {
		return fmt.Errorf("Unable to convert credentials into s3 credentials")
	}
	err = VerifyValidS3Creds(s3Creds)
	if err != nil {
		return err
	}
	err = uploadFile(s3Creds, file)
	if err != nil {
		return err
	}

	return nil
}

func (p *ExportPlugin) exportData(cliConnection plugin.CliConnection) error {
	// Get the services
	services, err := cliConnection.GetServices()
	if err != nil {
		return err
	}
	// See which service
	sources := p.findSupportedSources(services)
	if len(sources) < 1 {
		return fmt.Errorf("No supported sources\n")
	}
	source, err := p.promptServiceSelection(sources, "Input the number for the database service you want to export\n")
	if err != nil {
		return err
	}
	stores := p.findSupportedStores(services)
	if len(stores) < 1 {
		return fmt.Errorf("No supported stores\n")
	}
	store, err := p.promptServiceSelection(stores, "Input the number for the service you want to use to store the backup\n")
	if err != nil {
		return err
	}
	err = p.findDuplicateServices(source, store, cliConnection)
	if err != nil {
		return err
	}
	err = p.pushExportApp(cliConnection, source, store)
	if err != nil {
		return err
	}

	return nil
}

func getDefaultSources() []string {
	return []string{"mysql", "psql"}
}

func (p *ExportPlugin) importData(cliConnection plugin.CliConnection) error {
	entry, err := p.promptImportSelection("Input the number for the service you want to restore\n")
	if err != nil {
		return err
	}
	// Get the services that are available.
	services, err := cliConnection.GetServices()
	if err != nil {
		return err
	}
	// Filter for the services that could be used as destinations of where to restore the backup.

	types := p.findSupportedServiceFromPlan(entry.SourceServicePlan, getDefaultSources()...)
	targets := p.findSupportedServices(services, types...)
	if len(targets) < 1 {
		return fmt.Errorf("No supported destination services\n")
	}
	target, err := p.promptServiceSelection(targets, "Input the number for the database service you want to import data into.\n")
	if err != nil {
		return err
	}
	err = p.pushImportApp(cliConnection, target, entry)
	if err != nil {
		return err
	}
	return nil
}

func (p *ExportPlugin) promptImportSelection(prompt string) (ConfigEntry, error) {
	if len(p.config.Entries) < 1 {
		return ConfigEntry{}, fmt.Errorf("There are no conigured services to import data from in your local config. Please run `cf export-data` first.")
	}
	fmt.Printf("#\n")
	for i, entry := range p.config.Entries {
		fmt.Printf("%d\t| %s (API: \"%s\", Org \"%s\", Space \"%s\", Backup Location \"%s\")\n", i, entry.SourceServiceName, entry.API, entry.Org, entry.Space, entry.StoreServiceName)
	}
	fmt.Printf(prompt)
	i := -1
	_, err := fmt.Scan(&i)
	if err != nil {
		fmt.Printf("Inavlid input...\n")
		return ConfigEntry{}, err
	}
	if i < 0 || i >= len(p.config.Entries) {
		return ConfigEntry{}, fmt.Errorf("Number not in range\n")
	}
	return p.config.Entries[i], nil
}

func (p *ExportPlugin) findSupportedServiceFromPlan(plan string, serviceInstanceTypes ...string) []string {
	var supportedServices []string
	for _, serviceInstanceType := range serviceInstanceTypes {
		if strings.Contains(plan, serviceInstanceType) {
			supportedServices = append(supportedServices, serviceInstanceType)
		}
	}
	return supportedServices
}

func (p *ExportPlugin) findSupportedServices(services []plugin_models.GetServices_Model, serviceInstanceTypes ...string) []plugin_models.GetServices_Model {
	var supportedServices []plugin_models.GetServices_Model
	for _, service := range services {
		for _, serviceInstanceType := range serviceInstanceTypes {
			if strings.Contains(service.ServicePlan.Name, serviceInstanceType) {
				supportedServices = append(supportedServices, service)
			}
		}
	}
	return supportedServices
}

func (p *ExportPlugin) findSupportedStores(services []plugin_models.GetServices_Model) []plugin_models.GetServices_Model {
	var supportedStores []plugin_models.GetServices_Model
	for _, service := range services {
		if checkStoreCompatibility(service) {
			supportedStores = append(supportedStores, service)
		}
	}
	return supportedStores
}

func (p *ExportPlugin) findSupportedSources(services []plugin_models.GetServices_Model) []plugin_models.GetServices_Model {
	var supportedSources []plugin_models.GetServices_Model
	for _, service := range services {
		if checkSourceCompatibility(service) {
			supportedSources = append(supportedSources, service)
		}
	}
	return supportedSources
}

func (p *ExportPlugin) findDuplicateServices(source, store plugin_models.GetServices_Model, cliConnection plugin.CliConnection) error {
	api, _ := cliConnection.ApiEndpoint()
	org, _ := cliConnection.GetCurrentOrg()
	space, _ := cliConnection.GetCurrentSpace()
	for _, entry := range p.config.Entries {
		if api == entry.API && store.Guid == entry.StoreServiceGUID && source.Guid == entry.SourceServiceGUID && entry.SpaceGUID == space.Guid && entry.OrgGUID == org.Guid {
			return fmt.Errorf("There already exist a backup for service \"%s\" stored in serivce \"%s\" in org \"%s\" and space \"%s\" on API \"%s\". App \"%s\" moderated the migration. If this is old, please run \"cf clean-export-config\" command.", entry.SourceServiceName, entry.StoreServiceName, entry.Org, entry.Space, api, entry.AppName)
		}
	}
	return nil
}

func (p *ExportPlugin) promptServiceSelection(services []plugin_models.GetServices_Model, prompt string) (plugin_models.GetServices_Model, error) {
	fmt.Printf("#\t| Name\n")
	for i, service := range services {
		fmt.Printf("%d\t| %s\n", i, service.Name)
	}
	fmt.Printf(prompt)
	i := -1
	_, err := fmt.Scan(&i)
	if err != nil {
		fmt.Printf("Inavlid input...\n")
		return plugin_models.GetServices_Model{}, err
	}
	if i < 0 || i >= len(services) {
		return plugin_models.GetServices_Model{}, fmt.Errorf("Number not in range\n")
	}
	return services[i], nil
}

func (p *ExportPlugin) pushImportApp(cliConnection plugin.CliConnection, target plugin_models.GetServices_Model, entry ConfigEntry) error {
	importProgram, err := Asset(filepath.Join("import", "import.py"))
	if err != nil {
		return fmt.Errorf("Unable to find import.py")
	}
	manifestData, err := Asset(filepath.Join("import", "manifest.yml"))
	if err != nil {
		return fmt.Errorf("Unable to find manifest.yml")
	}
	commonPy, err := Asset(filepath.Join("pycommon", "common.py"))
	if err != nil {
		return fmt.Errorf("Unable to find common.py")
	}

	dir, err := ioutil.TempDir("", "export-data-plugin")
	if err != nil {
		return err
	}
	procfile, err := Asset(filepath.Join("export", "Procfile"))
	if err != nil {
		return fmt.Errorf("Unable to find export.py")
	}
	err = ioutil.WriteFile(filepath.Join(dir, "Procfile"), procfile, 0664)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(dir, "import.py"), importProgram, 0664)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(dir, "common.py"), commonPy, 0664)
	if err != nil {
		return err
	}
	// Replace services in manifest
	manifestData = bytes.Replace(manifestData, []byte("REPLACE_TARGET"), []byte(target.Name), -1)
	manifestData = bytes.Replace(manifestData, []byte("REPLACETARGETSERVICE"), []byte(target.Name), -1)
	manifestData = bytes.Replace(manifestData, []byte("REPLACESTORETYPE"), []byte(entry.StoreServiceType), -1)
	creds, _ := json.Marshal(entry.Credentials)
	manifestData = bytes.Replace(manifestData, []byte("REPLACECREDENTIALS"), []byte(fmt.Sprintf("'%s'", string(creds))), -1)
	err = ioutil.WriteFile(filepath.Join(dir, "manifest.yml"), manifestData, 0664)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	appName := "import-db-" + uniuri.New()

	_, err = cliConnection.CliCommand("push", appName, "-p", dir, "-f", filepath.Join(dir, "manifest.yml"))
	if err != nil {
		return err
	}

	return nil
}

func (p *ExportPlugin) pushExportApp(cliConnection plugin.CliConnection, source, store plugin_models.GetServices_Model) error {
	exportProgram, err := Asset(filepath.Join("export", "export.py"))
	if err != nil {
		return fmt.Errorf("Unable to find export.py")
	}
	manifestData, err := Asset(filepath.Join("export", "manifest.yml"))
	if err != nil {
		return fmt.Errorf("Unable to find manifest.yml")
	}
	commonPy, err := Asset(filepath.Join("pycommon", "common.py"))
	if err != nil {
		return fmt.Errorf("Unable to find common.py")
	}
	dir, err := ioutil.TempDir("", "export-data-plugin")
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(dir, "export.py"), exportProgram, 0664)
	if err != nil {
		return err
	}
	procfile, err := Asset(filepath.Join("export", "Procfile"))
	if err != nil {
		return fmt.Errorf("Unable to find export.py")
	}
	err = ioutil.WriteFile(filepath.Join(dir, "Procfile"), procfile, 0664)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(dir, "common.py"), commonPy, 0664)
	if err != nil {
		return err
	}
	// Replace services in manifest
	manifestData = bytes.Replace(manifestData, []byte("REPLACE_STORE"), []byte(store.Name), -1)
	manifestData = bytes.Replace(manifestData, []byte("REPLACE_SOURCE"), []byte(source.Name), -1)
	manifestData = bytes.Replace(manifestData, []byte("REPLACESOURCESERVICE"), []byte(source.Name), -1)
	err = ioutil.WriteFile(filepath.Join(dir, "manifest.yml"), manifestData, 0664)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	appName := "export-db-" + uniuri.New()

	_, err = cliConnection.CliCommand("push", appName, "-p", dir, "-f", filepath.Join(dir, "manifest.yml"))
	if err != nil {
		return err
	}

	app, err := cliConnection.GetApp(appName)
	if err != nil {
		return err
	}
	service, err := p.getVCAPServicesEnv(cliConnection, app, store)
	if err != nil {
		return err
	}
	org, _ := cliConnection.GetCurrentOrg()
	space, _ := cliConnection.GetCurrentSpace()
	api, _ := cliConnection.ApiEndpoint()
	apiVersion, _ := cliConnection.ApiVersion()
	p.config.Entries = append(p.config.Entries, ConfigEntry{AppName: app.Name, AppGUID: app.Guid,
		Org: org.Name, OrgGUID: org.Guid, Space: space.Name, SpaceGUID: space.Guid, API: api,
		APIVersion:        apiVersion,
		SourceServiceGUID: source.Guid, SourceServiceName: source.Name, SourceServicePlan: source.ServicePlan.Name, SourceServiceType: source.Service.Name,
		StoreServiceGUID: store.Guid, StoreServiceType: service.GetType(), StoreServiceName: service.GetName(),
		Credentials: service.GetCredentials()})

	return nil
}

// Similar to https://github.com/jthomas/copyenv/blob/master/copyenv.go#L30
// Asked author to make it library so that we could import that logic.
// Right now, we can't because it's in the main package.
// https://github.com/jthomas/copyenv/issues/7
func (p *ExportPlugin) getVCAPServicesEnv(cliConnection plugin.CliConnection, app plugin_models.GetAppModel, store plugin_models.GetServices_Model) (Service, error) {
	// Get the env vars from a cf curl.
	out, err := cliConnection.CliCommandWithoutTerminalOutput("curl", fmt.Sprintf("/v2/apps/%s/env", app.Guid))
	if err != nil {
		return nil, err
	}
	output := strings.Join(out, "")
	if !strings.Contains(output, "VCAP_SERVICES") {
		return nil, fmt.Errorf("Unable to find VCAP_SERVICES in environment vars for app %s", app.Name)
	}
	env := models.NewEnvironment()
	err = json.Unmarshal([]byte(output), &env)
	if err != nil {
		return nil, fmt.Errorf("Unable to find `system_env_json` in environment vars for app %s", app.Name)
	}
	vcap, ok := env.System["VCAP_SERVICES"].(map[string]interface{})
	if !ok || len(vcap) < 1 {
		return nil, fmt.Errorf("Unable to find VCAP_SERVICES in environment vars for app %s", app.Name)
	}
	if _, ok := vcap["s3"]; ok {
		return createS3Store(vcap, app, store)
	}
	return nil, fmt.Errorf("Couldn't find a service type to store to find the vcap service env vars for service %s in app %s", store.Name, app.Name)
}
