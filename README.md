VMware InfluxDB Collector
=========

Requirements
------------

* [Glide](https://github.com/Masterminds/glide)
* [Docker](https://www.docker.com/)
* [Ansible](https://www.ansible.com/)


Dependencies
------------

* [govmomi](https://github.com/vmware/govmomi)
* [InfluxDb](https://github.com/influxdata/influxdb)
* [Spew](https://github.com/davecgh/go-spew)

We use glide to vendor our dependencies.

```
$ make glide
```

Getting Started
-----

The make task below will stand up a Docker container and build vsphere-influxdb-go for each OS type.

```
$ make setup
```

You should have a `bin` directory now with your built binaries.

```
bin
├── darwin-amd64
│   └── vsphere-influxdb-go
├── linux-amd64
│   └── vsphere-influxdb-go
└── windows-amd64
    └── vsphere-influxdb-go.exe
```

Example Configuration
----------------

The configuration file that the binary reads from by default is located `/etc/vsphere-influxdb-go.json`
You can pass a config file of your own with the `-config` flag.



```
{
	"Domain": "",
	"Interval": 60,
	"VCenters": [
		{ "Username": "AwesomeUser", "Password": "SuperSekretPassword", "Hostname": "vc01.domain.com" }

	],
	"InfluxDB": {
		"Hostname": "http://influxdb.domain.com:8086",
		"Username": "InfluxUser",
		"Password": "SuperSekretPassword",
		"Database": "vmware"
	},
	"Metrics": [
		{
			"ObjectType": [ "VirtualMachine", "HostSystem" ],
			"Definition": [
				{ "Metric": "cpu.usage.average", "Instances": "*" },
				{ "Metric": "cpu.usage.maximum", "Instances": "*" },
				{ "Metric": "cpu.usagemhz.average", "Instances": "*" },
				{ "Metric": "cpu.usagemhz.maximum", "Instances": "*" },
				{ "Metric": "cpu.wait.summation", "Instances": "*" },
				{ "Metric": "cpu.system.summation", "Instances": "*" },
				{ "Metric": "cpu.ready.summation", "Instances": "*" },
				{ "Metric": "mem.usage.average", "Instances": "*" },
				{ "Metric": "mem.usage.maximum", "Instances": "*" },
				{ "Metric": "mem.consumed.average", "Instances": "*" },
				{ "Metric": "mem.consumed.maximum", "Instances": "*" },
				{ "Metric": "mem.active.average", "Instances": "*" },
				{ "Metric": "mem.active.maximum", "Instances": "*" },
				{ "Metric": "mem.vmmemctl.average", "Instances": "*" },
				{ "Metric": "mem.vmmemctl.maximum", "Instances": "*" },
				{ "Metric": "mem.totalCapacity.average", "Instances": "*" },
				{ "Metric": "net.packetsRx.summation", "Instances": "*" },
			  { "Metric": "net.packetsTx.summation", "Instances": "*" },
				{ "Metric": "net.throughput.usage.average", "Instances": "*" },
				{ "Metric": "net.received.average", "Instances": "*" },
				{ "Metric": "net.transmitted.average", "Instances": "*" },
				{ "Metric": "net.throughput.usage.nfs.average", "Instances": "*" },
				{ "Metric": "datastore.numberReadAveraged.average", "Instances": "*" },
				{ "Metric": "datastore.numberWriteAveraged.average", "Instances": "*" },
				{ "Metric": "datastore.read.average", "Instances": "*" },
				{ "Metric": "datastore.write.average", "Instances": "*" },
				{ "Metric": "datastore.totalReadLatency.average", "Instances": "*" },
				{ "Metric": "datastore.totalWriteLatency.average", "Instances": "*" },
				{ "Metric": "mem.capacity.provisioned.average", "Instances": "*"},
				{ "Metric": "cpu.corecount.provisioned.average", "Instances": "*" }
			]
		},
		{
			"ObjectType": [ "VirtualMachine" ],
			"Definition": [
			{ "Metric": "datastore.datastoreVMObservedLatency.latest", "Instances": "*" }
			]
		},
		{
			"ObjectType": [ "HostSystem" ],
			"Definition": [
				{ "Metric": "disk.maxTotalLatency.latest", "Instances": "" },
				{ "Metric": "disk.numberReadAveraged.average", "Instances": "*" },
				{ "Metric": "disk.numberWriteAveraged.average", "Instances": "*" },
				{ "Metric": "net.throughput.contention.summation", "Instances": "*" }
			]
		}
	]
}
```

Example Usage
--------------

Below is an example of running the binary with a configuration file.

```
$ /path/to/vsphere-influxdb-go -config /path/to/config.json
```

You can alternatively run this as a Jenkins job or crontab.

```
* * * * * /path/to/vsphere-influxdb-go -config /path/to/config.json >> /var/log/cron.log 2>&1
```
