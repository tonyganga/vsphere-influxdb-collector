package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	influxclient "github.com/influxdata/influxdb/client/v2"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

const (
	// name of the service
	name        = "vsphere-influxdb"
	description = "send vsphere stats to influxdb"
)

// Configuration is used to store config data
type Configuration struct {
	VCenters []*VCenter
	Metrics  []Metric
	Interval int
	Domain   string
	InfluxDB InfluxDB
}

// InfluxDB is used for InfluxDB connections
type InfluxDB struct {
	Hostname string
	Username string
	Password string
	Database string
}

// VCenter for VMware vCenter connections
type VCenter struct {
	Hostname     string
	Username     string
	Password     string
	MetricGroups []*MetricGroup
}

// MetricDef metric definition
type MetricDef struct {
	Metric    string
	Instances string
	Key       int32
}

// Metric is used for metrics retrieval
type Metric struct {
	ObjectType []string
	Definition []MetricDef
}

// MetricGroup is used for grouping metrics retrieval
type MetricGroup struct {
	ObjectType string
	Metrics    []MetricDef
	Mor        []types.ManagedObjectReference
}

// EntityQuery are informations to query about an entity
type EntityQuery struct {
	Name    string
	Entity  types.ManagedObjectReference
	Metrics []int32
}

var debug bool
var stdlog, errlog *log.Logger

// Connect to the actual vCenter connection used to query data
func (vcenter *VCenter) Connect() (*govmomi.Client, error) {
	// Prepare vCenter Connections
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdlog.Println("connecting to vcenter: " + vcenter.Hostname)
	u, err := url.Parse("https://" + vcenter.Username + ":" + vcenter.Password + "@" + vcenter.Hostname + "/sdk")
	if err != nil {
		errlog.Println("Could not parse vcenter url: ", vcenter.Hostname)
		errlog.Println("Error: ", err)
		return nil, err
	}

	client, err := govmomi.NewClient(ctx, u, true)
	if err != nil {
		errlog.Println("Could not connect to vcenter: ", vcenter.Hostname)
		errlog.Println("Error: ", err)
		return nil, err
	}

	return client, nil
}

// Init the VCenter connection
func (vcenter *VCenter) Init(config Configuration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := vcenter.Connect()
	if err != nil {
		errlog.Println("Could not connect to vcenter: ", vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}
	defer client.Logout(ctx)

	var perfmanager mo.PerformanceManager
	err = client.RetrieveOne(ctx, *client.ServiceContent.PerfManager, nil, &perfmanager)
	if err != nil {
		errlog.Println("Could not get performance manager")
		errlog.Println("Error: ", err)
		return
	}

	for _, perf := range perfmanager.PerfCounter {
		groupinfo := perf.GroupInfo.GetElementDescription()
		nameinfo := perf.NameInfo.GetElementDescription()
		identifier := groupinfo.Key + "." + nameinfo.Key + "." + fmt.Sprint(perf.RollupType)
		for _, metric := range config.Metrics {
			for _, metricdef := range metric.Definition {
				if metricdef.Metric == identifier {
					metricd := MetricDef{Metric: metricdef.Metric, Instances: metricdef.Instances, Key: perf.Key}
					for _, mtype := range metric.ObjectType {
						added := false
						for _, metricgroup := range vcenter.MetricGroups {
							if metricgroup.ObjectType == mtype {
								metricgroup.Metrics = append(metricgroup.Metrics, metricd)
								added = true
								break
							}
						}
						if added == false {
							metricgroup := MetricGroup{ObjectType: mtype, Metrics: []MetricDef{metricd}}
							vcenter.MetricGroups = append(vcenter.MetricGroups, &metricgroup)
						}
					}
				}
			}
		}
	}
}

// Query a vcenter
func (vcenter *VCenter) Query(config Configuration, InfluxDBClient influxclient.Client) {
	stdlog.Println("Setting up query inventory of vcenter: ", vcenter.Hostname)

	// Create the contect
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get the client
	client, err := vcenter.Connect()
	if err != nil {
		errlog.Println("Could not connect to vcenter: ", vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}
	defer client.Logout(ctx)

	// Create the view manager
	var viewManager mo.ViewManager
	err = client.RetrieveOne(ctx, *client.ServiceContent.ViewManager, nil, &viewManager)
	if err != nil {
		errlog.Println("Could not get view manager from vcenter: " + vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}

	// Get the Datacenters from root folder
	var rootFolder mo.Folder
	err = client.RetrieveOne(ctx, client.ServiceContent.RootFolder, nil, &rootFolder)
	if err != nil {
		errlog.Println("Could not get root folder from vcenter: " + vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}

	datacenters := []types.ManagedObjectReference{}
	for _, child := range rootFolder.ChildEntity {
		if child.Type == "Datacenter" {
			datacenters = append(datacenters, child)
		}
	}
	// Get intresting object types from specified queries
	objectTypes := []string{}
	for _, group := range vcenter.MetricGroups {
		objectTypes = append(objectTypes, group.ObjectType)
	}
	objectTypes = append(objectTypes, "ClusterComputeResource")
	objectTypes = append(objectTypes, "ResourcePool")

	// Loop trought datacenters and create the intersting object reference list
	mors := []types.ManagedObjectReference{}
	for _, datacenter := range datacenters {
		// Create the CreateContentView request
		req := types.CreateContainerView{This: viewManager.Reference(), Container: datacenter, Type: objectTypes, Recursive: true}
		res, err := methods.CreateContainerView(ctx, client.RoundTripper, &req)
		if err != nil {
			errlog.Println("Could not create container view from vcenter: " + vcenter.Hostname)
			errlog.Println("Error: ", err)
			continue
		}
		// Retrieve the created ContentView
		var containerView mo.ContainerView
		err = client.RetrieveOne(ctx, res.Returnval, nil, &containerView)
		if err != nil {
			errlog.Println("Could not get container view from vcenter: " + vcenter.Hostname)
			errlog.Println("Error: ", err)
			continue
		}
		// Add found object to object list
		mors = append(mors, containerView.View...)
	}

	// Create MORS for each object type
	vmRefs := []types.ManagedObjectReference{}
	hostRefs := []types.ManagedObjectReference{}
	clusterRefs := []types.ManagedObjectReference{}
	respoolRefs := []types.ManagedObjectReference{}

	newMors := []types.ManagedObjectReference{}

	spew.Dump(mors)
	// Assign each MORS type to a specific array
	for _, mor := range mors {
		if mor.Type == "VirtualMachine" {
			vmRefs = append(vmRefs, mor)
			newMors = append(newMors, mor)
		} else if mor.Type == "HostSystem" {
			hostRefs = append(hostRefs, mor)
			newMors = append(newMors, mor)
		} else if mor.Type == "ClusterComputeResource" {
			clusterRefs = append(clusterRefs, mor)
		} else if mor.Type == "ResourcePool" {
			respoolRefs = append(respoolRefs, mor)
		}
	}
	// Copy the mors without the clusters
	mors = newMors

	pc := property.DefaultCollector(client.Client)

	// Retrieve properties for all vms
	var vmmo []mo.VirtualMachine
	err = pc.Retrieve(ctx, vmRefs, []string{"summary"}, &vmmo)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Retrieve properties for hosts
	var hsmo []mo.HostSystem
	err = pc.Retrieve(ctx, hostRefs, []string{"summary"}, &hsmo)
	if err != nil {
		fmt.Println(err)
		return
	}

	//Retrieve properties for ResourcePool
	var rpmo []mo.ResourcePool
	err = pc.Retrieve(ctx, respoolRefs, []string{"summary"}, &rpmo)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Initialize the map that will hold the VM MOR to ResourcePool reference
	vmToPool := make(map[types.ManagedObjectReference]string)

	// Retrieve properties for ResourcePools
	if len(respoolRefs) > 0 {
		if debug == true {
			stdlog.Println("going inside ResourcePools")
		}
		var respool []mo.ResourcePool
		err = pc.Retrieve(ctx, respoolRefs, []string{"name", "config", "vm"}, &respool)
		if err != nil {
			fmt.Println(err)
			return
		}
		for _, pool := range respool {
			stdlog.Println(pool.Config.MemoryAllocation.GetResourceAllocationInfo().Limit)
			stdlog.Println(pool.Config.CpuAllocation.GetResourceAllocationInfo().Limit)
			if debug == true {
				stdlog.Println("---resourcepool name - you should see every resourcepool here (+VMs inside)----")
				stdlog.Println(pool.Name)
			}
			for _, vm := range pool.Vm {
				if debug == true {
					stdlog.Println("--VM ID - you should see every VM ID here--")
					stdlog.Println(vm)
				}
				vmToPool[vm] = pool.Name
			}
		}
	}

	// Initialize the map that will hold the VM MOR to cluster reference
	vmToCluster := make(map[types.ManagedObjectReference]string)

	// Retrieve properties for clusters, if any
	if len(clusterRefs) > 0 {
		if debug == true {
			stdlog.Println("going inside clusters")
		}
		var clmo []mo.ClusterComputeResource
		err = pc.Retrieve(ctx, clusterRefs, []string{"name", "configuration"}, &clmo)
		if err != nil {
			fmt.Println(err)
			return
		}
		for _, cl := range clmo {
			if debug == true {
				stdlog.Println("---cluster name - you should see every cluster here---")
				stdlog.Println(cl.Name)
				stdlog.Println("You should see the cluster object, cluster configuration object, and cluster configuration dasvmconfig which should contain all VMs")
				spew.Dump(cl)
				spew.Dump(cl.Configuration)
				spew.Dump(cl.Configuration.DasVmConfig)
			}

			for _, vm := range cl.Configuration.DasVmConfig {
				if debug == true {
					stdlog.Println("--VM ID - you should see every VM ID here--")
					stdlog.Println(vm.Key.Value)
				}

				vmToCluster[vm.Key] = cl.Name
			}
		}
	}

	// Retrieve properties for the pools
	respoolSummary := make(map[types.ManagedObjectReference]map[string]string)
	for _, pools := range rpmo {
		respoolSummary[pools.Self] = make(map[string]string)
		respoolSummary[pools.Self]["name"] = pools.Summary.GetResourcePoolSummary().Name
	}

	// Retrieve properties for the hosts
	hostSummary := make(map[types.ManagedObjectReference]map[string]string)
	hostExtraMetrics := make(map[types.ManagedObjectReference]map[string]int64)

	for _, host := range hsmo {
		hostSummary[host.Self] = make(map[string]string)
		hostSummary[host.Self]["name"] = host.Summary.Config.Name
		hostExtraMetrics[host.Self] = make(map[string]int64)
		hostExtraMetrics[host.Self]["cpu_corecount_total"] = int64(host.Summary.Hardware.NumCpuThreads)
	}

	// Initialize the map that will hold all extra tags
	vmSummary := make(map[types.ManagedObjectReference]map[string]string)

	// Assign extra details per VM in vmSummary
	for _, vm := range vmmo {
		vmSummary[vm.Self] = make(map[string]string)
		// Ugly way to extract datastore value
		re, err := regexp.Compile(`\[(.*?)\]`)
		if err != nil {
			fmt.Println(err)
		}
		vmSummary[vm.Self]["datastore"] = strings.Replace(strings.Replace(re.FindString(fmt.Sprintln(vm.Summary.Config)), "[", "", -1), "]", "", -1)
		if vmToCluster[vm.Self] != "" {
			vmSummary[vm.Self]["cluster"] = vmToCluster[vm.Self]
		}
		if vmToPool[vm.Self] != "" {
			vmSummary[vm.Self]["respool"] = vmToPool[vm.Self]
		}
		vmSummary[vm.Self]["esx"] = hostSummary[*vm.Summary.Runtime.Host]["name"]
	}

	// get object names
	objects := []mo.ManagedEntity{}

	//object for propery collection
	propSpec := &types.PropertySpec{Type: "ManagedEntity", PathSet: []string{"name"}}
	var objectSet []types.ObjectSpec
	for _, mor := range mors {
		objectSet = append(objectSet, types.ObjectSpec{Obj: mor, Skip: types.NewBool(false)})
	}

	//retrieve name property
	propreq := types.RetrieveProperties{SpecSet: []types.PropertyFilterSpec{{ObjectSet: objectSet, PropSet: []types.PropertySpec{*propSpec}}}}
	propres, err := client.PropertyCollector().RetrieveProperties(ctx, propreq)
	if err != nil {
		errlog.Println("Could not retrieve object names from vcenter: " + vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}

	//load retrieved properties
	err = mo.LoadRetrievePropertiesResponse(propres, &objects)
	if err != nil {
		errlog.Println("Could not retrieve object names from vcenter: " + vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}

	//create a map to resolve object names
	morToName := make(map[types.ManagedObjectReference]string)
	for _, object := range objects {
		morToName[object.Self] = object.Name
	}

	//create a map to resolve metric names
	metricToName := make(map[int32]string)
	for _, metricgroup := range vcenter.MetricGroups {
		for _, metricdef := range metricgroup.Metrics {
			metricToName[metricdef.Key] = metricdef.Metric
		}
	}

	// Create Queries from interesting objects and requested metrics

	queries := []types.PerfQuerySpec{}

	// Common parameters
	intervalIDint := 20
	var intervalID int32
	intervalID = int32(intervalIDint)

	endTime := time.Now().Add(time.Duration(-1) * time.Second)
	startTime := endTime.Add(time.Duration(-config.Interval) * time.Second)

	// Parse objects
	for _, mor := range mors {
		metricIds := []types.PerfMetricId{}
		for _, metricgroup := range vcenter.MetricGroups {
			if metricgroup.ObjectType == mor.Type {
				for _, metricdef := range metricgroup.Metrics {
					metricIds = append(metricIds, types.PerfMetricId{CounterId: metricdef.Key, Instance: metricdef.Instances})
				}
			}
		}
		queries = append(queries, types.PerfQuerySpec{Entity: mor, StartTime: &startTime, EndTime: &endTime, MetricId: metricIds, IntervalId: intervalID})
	}

	// Query the performances
	perfreq := types.QueryPerf{This: *client.ServiceContent.PerfManager, QuerySpec: queries}
	perfres, err := methods.QueryPerf(ctx, client.RoundTripper, &perfreq)
	if err != nil {
		errlog.Println("Could not request perfs from vcenter: " + vcenter.Hostname)
		errlog.Println("Error: ", err)
		return
	}

	// Get the result
	vcName := strings.Replace(vcenter.Hostname, config.Domain, "", -1)

	//Influx batch points
	bp, err := influxclient.NewBatchPoints(influxclient.BatchPointsConfig{
		Database:  config.InfluxDB.Database,
		Precision: "s",
	})
	if err != nil {
		errlog.Println(err)
		return
	}

	for _, base := range perfres.Returnval {
		pem := base.(*types.PerfEntityMetric)
		entityName := strings.ToLower(pem.Entity.Type)
		name := strings.ToLower(strings.Replace(morToName[pem.Entity], config.Domain, "", -1))

		//Create map for InfluxDB fields
		fields := make(map[string]interface{})

		// Create map for InfluxDB tags
		tags := map[string]string{"host": vcName, "name": name}

		// Add extra per VM tags
		if summary, ok := vmSummary[pem.Entity]; ok {
			for key, tag := range summary {
				tags[key] = tag
			}
		}
		if summary, ok := hostSummary[pem.Entity]; ok {
			for key, tag := range summary {
				tags[key] = tag
			}
		}

		if summary, ok := respoolSummary[pem.Entity]; ok {
			for key, tag := range summary {
				tags[key] = tag
			}
		}

		specialFields := make(map[string]map[string]map[string]map[string]interface{})
		specialTags := make(map[string]map[string]map[string]map[string]string)
		nowTime := time.Now()
		for _, baseserie := range pem.Value {
			serie := baseserie.(*types.PerfMetricIntSeries)
			metricName := strings.ToLower(metricToName[serie.Id.CounterId])
			influxMetricName := strings.Replace(metricName, ".", "_", -1)
			instanceName := strings.ToLower(strings.Replace(serie.Id.Instance, ".", "_", -1))
			measurementName := strings.Split(metricName, ".")[0]

			if strings.Index(influxMetricName, "datastore") != -1 {
				instanceName = ""
			}

			var value int64 = -1
			if strings.HasSuffix(metricName, ".average") {
				value = average(serie.Value...)
			} else if strings.HasSuffix(metricName, ".maximum") {
				value = max(serie.Value...)
			} else if strings.HasSuffix(metricName, ".minimum") {
				value = min(serie.Value...)
			} else if strings.HasSuffix(metricName, ".latest") {
				value = serie.Value[len(serie.Value)-1]
			} else if strings.HasSuffix(metricName, ".summation") {
				value = sum(serie.Value...)
			}

			if instanceName == "" {
				fields[influxMetricName] = value
			} else {
				// init maps
				if specialFields[measurementName] == nil {
					specialFields[measurementName] = make(map[string]map[string]map[string]interface{})
					specialTags[measurementName] = make(map[string]map[string]map[string]string)

				}

				if specialFields[measurementName][tags["name"]] == nil {
					specialFields[measurementName][tags["name"]] = make(map[string]map[string]interface{})
					specialTags[measurementName][tags["name"]] = make(map[string]map[string]string)
				}

				if specialFields[measurementName][tags["name"]][instanceName] == nil {
					specialFields[measurementName][tags["name"]][instanceName] = make(map[string]interface{})
					specialTags[measurementName][tags["name"]][instanceName] = make(map[string]string)

				}

				specialFields[measurementName][tags["name"]][instanceName][influxMetricName] = value

				for k, v := range tags {
					specialTags[measurementName][tags["name"]][instanceName][k] = v
				}
				specialTags[measurementName][tags["name"]][instanceName]["instance"] = instanceName
			}
		}

		if metrics, ok := hostExtraMetrics[pem.Entity]; ok {
			for key, value := range metrics {
				fields[key] = value
			}
		}

		//create InfluxDB points
		pt, err := influxclient.NewPoint(entityName, tags, fields, nowTime)
		if err != nil {
			errlog.Println(err)
			continue
		}
		bp.AddPoint(pt)

		for measurement, v := range specialFields {
			for name, metric := range v {
				for instance, value := range metric {
					pt2, err := influxclient.NewPoint(measurement, specialTags[measurement][name][instance], value, time.Now())
					if err != nil {
						errlog.Println(err)
						continue
					}
					bp.AddPoint(pt2)
				}
			}
		}

		var respool []mo.ResourcePool
		err = pc.Retrieve(ctx, respoolRefs, []string{"name", "config", "vm"}, &respool)
		if err != nil {
			errlog.Println(err)
			continue
		}

		for _, pool := range respool {
			respoolFields := map[string]interface{}{
				"cpu_limit":    pool.Config.CpuAllocation.GetResourceAllocationInfo().Limit,
				"memory_limit": pool.Config.MemoryAllocation.GetResourceAllocationInfo().Limit,
			}
			respoolTags := map[string]string{"pool_name": pool.Name}
			pt3, err := influxclient.NewPoint("resourcepool", respoolTags, respoolFields, time.Now())
			if err != nil {
				errlog.Println(err)
				continue
			}
			bp.AddPoint(pt3)
		}

	}

	//InfluxDB send
	err = InfluxDBClient.Write(bp)
	if err != nil {
		errlog.Println(err)
		return
	}

	stdlog.Println("sent data to Influxdb")
}

func min(n ...int64) int64 {
	var min int64 = -1
	for _, i := range n {
		if i >= 0 {
			if min == -1 {
				min = i
			} else {
				if i < min {
					min = i
				}
			}
		}
	}
	return min
}

func max(n ...int64) int64 {
	var max int64 = -1
	for _, i := range n {
		if i >= 0 {
			if max == -1 {
				max = i
			} else {
				if i > max {
					max = i
				}
			}
		}
	}
	return max
}

func sum(n ...int64) int64 {
	var total int64
	for _, i := range n {
		if i > 0 {
			total += i
		}
	}
	return total
}

func average(n ...int64) int64 {
	var total int64
	var count int64
	for _, i := range n {
		if i >= 0 {
			count++
			total += i
		}
	}
	favg := float64(total) / float64(count)
	return int64(math.Floor(favg + .5))
}

func queryVCenter(vcenter VCenter, config Configuration, InfluxDBClient influxclient.Client) {
	stdlog.Println("Querying vcenter")
	vcenter.Query(config, InfluxDBClient)
}

func main() {
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	var cfgFile = flag.String("config", "/etc/"+path.Base(os.Args[0])+".json", "Config file to use. Default is /etc/"+path.Base(os.Args[0])+".json")
	flag.Parse()

	stdlog = log.New(os.Stdout, "", log.Ldate|log.Ltime)
	errlog = log.New(os.Stderr, "", log.Ldate|log.Ltime)

	stdlog.Println("Starting :", path.Base(os.Args[0]))

	// read the configuration
	file, err := os.Open(*cfgFile)
	if err != nil {
		errlog.Println("Could not open configuration file", *cfgFile)
		errlog.Fatalln(err)
	}

	jsondec := json.NewDecoder(file)
	config := Configuration{}
	err = jsondec.Decode(&config)
	if err != nil {
		errlog.Println("Could not decode configuration file", *cfgFile)
		errlog.Fatalln(err)
	}

	for _, vcenter := range config.VCenters {
		vcenter.Init(config)
	}

	InfluxDBClient, err := influxclient.NewHTTPClient(influxclient.HTTPConfig{
		Addr:     config.InfluxDB.Hostname,
		Username: config.InfluxDB.Username,
		Password: config.InfluxDB.Password,
	})
	if err != nil {
		errlog.Println("Could not connect to InfluxDB")
		errlog.Fatalln(err)
	}

	stdlog.Println("Successfully connected to Influx")

	for _, vcenter := range config.VCenters {
		queryVCenter(*vcenter, config, InfluxDBClient)
	}
}
