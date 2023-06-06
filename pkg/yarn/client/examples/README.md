# Examples
use github.com/hortonworks/gohadoop as native go clients for Apache Hadoop YARN.

It includes an early version of Hadoop IPC client and requisite YARN client libraries to implement YARN applications completely in go (both YARN application-client and application-master).

Koordinator implements resource manager administration service client.

# Notes:
Set HADOOP_CONF_DIR environment variable, and ensure the conf directory contains both *-default.xml and *-site.xml files.
rm_update_node_resource.go is an example go YARN rpc client of rm-admin: call update node resource to do the updates.
To run:

# Run rm_update_node_resource
$ HADOOP_CONF_DIR=conf go run pkg/yarn/client/examples/rm_update_node_resource.go