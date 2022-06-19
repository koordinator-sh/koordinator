## Logging Conventions

This document provides an overview of the recommended way to develop and implement logging for components of
Koordinator. The following conventions for the klog levels to use. [klog](https://pkg.go.dev/github.com/kubernetes/klog)
is globally preferred to [log](https://pkg.go.dev/log) for better runtime control.

* klog.Errorf() - Always an error

* klog.Warningf() - Something unexpected, but probably not an error

* klog.Infof() has multiple levels:
    * klog.V(0) - Generally useful for this to ALWAYS be visible to an operator
        * Programmer errors
        * Logging extra info about a panic
        * CLI argument handling
    * klog.V(1) - A reasonable default log level if you don't want verbosity.
        * Information about config (listening on X, watching Y)
        * Errors that repeat frequently that relate to conditions that can be corrected (pod detected as unhealthy)
    * klog.V(2) - Useful steady state information about the service and important log messages that may correlate to
      significant changes in the system. This is the recommended default log level for most systems.
        * Logging HTTP requests and their exit code
        * System state changing (killing pod)
        * Controller state change events (starting pods)
        * Scheduler log messages
    * klog.V(3) - Extended information about changes
        * More info about system state changes
    * klog.V(4) - Debug level verbosity
        * Logging in particularly thorny parts of code where you may want to come back later and check it
    * klog.V(5) - Trace level verbosity
        * Context to understand the steps leading up to errors and warnings
        * More information for troubleshooting reported issues

* klog.InfoS() - structured logs to the INFO log

As per the comments, the practical default level is V(2). Developers and QE environments may wish to run at V(3) or V(4)
. If you wish to change the log level, you can pass in `-v=X` where X is the desired maximum level to log.

## Logging header formats

An example logging message is

```
I0528 19:15:22.737538   47512 logtest.go:52] Pod kube-system/kube-dns status was updated to ready
```

Explanation of header:

```
Lmmdd hh:mm:ss.uuuuuu threadid file:line] msg...

where the fields are defined as follows:
	L                A single character, representing the log level (eg 'I' for INFO)
	mm               The month (zero padded; ie May is '05')
	dd               The day (zero padded)
	hh:mm:ss.uuuuuu  Time in hours, minutes and fractional seconds
	threadid         The space-padded thread ID as returned by GetTID()
	file             The file name
	line             The line number
	msg              The user-supplied message
```

See more in [here](https://github.com/kubernetes/klog/blob/a585df903e8653af246bcb8291cc856af2df9cfd/klog.go#L527-L543)
