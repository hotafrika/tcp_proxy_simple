## TCP proxy (version 1)

### Main idea
The main simple idea is to create two IO goroutines for every pair of connections and execute directional copying from one connection to another and vice-versa.

### Summary
This is a simple multi-backend and multi-frontend TCP proxy. The proxy is configured using JSON config file and available flags.
Config file contains configuration info about available "apps", every "app" has 1-N ports ("frontends") and 1-M "backends".

Let's take a look at all details:

Graceful shutdown is implemented. NotifyContext is used to control the execution of all goroutines and some operations.
On start, TCP proxy starts service goroutines for every frontend and backend.

On start, every frontend tries to listen on the specified port. If the port is busy, frontend will continue to try to create a listener on the port until success or ctx is done.
When the listener is successfully created, frontend starts to accept new incoming connections.

On start, every backend starts the goroutine with healthcheck (active healthcheck) to know if the backend endpoint is available. 
Also, when a new connection to the backend endpoint failed, so this backend gets "unavailable" state (passive healthcheck) until next successful active healthcheck.

On a new incoming connection to a frontend, its app checks if there are available backends. The app chooses the backend with the least active connections.
Then, a new remote connection is created to the chosen backend endpoint.
Then two goroutines are created to serve this connection pair "incoming connection"-"remote connection".
One goroutine copies data from "incoming connection"--->"remote connection". The second copies data from "remote connection"--->"incoming connection".

IO operations stop on an error or 0 bytes copying. The IO goroutine exits and both connections are closed (as a result another IO goroutine exits).

To copy data between two connections io.CopyBuffer is used in combination with sync.Pool for buffers. It allows for decreasing memory allocations.
This logic still can be reviewed because in some systems io.CopyBuffer after unsuccessful attempts of splice ((c *TCPConn) readFrom()) falls back to io.Copy with its buffer 32kB for every call.

This TCP proxy is straightforward and robust. It tested under a load of tens of thousands of connections and works predictably.

### Available flags:
* -config FILENAME - path to the JSON config file, default "config.json";
* -loglevel LEVEL - log level, default 0. Possible values range is 0-7, where 0=debug, 1=info, 2=warn, 3=error, 4=fatal, 5=panic, .. 7=disabled;
* -pprof - starts pprof web server on port 6060.

### Launch examples:

Starts proxy with loglevel=debug, config filepath "config.json", without pprof enabled.
```bash
cmd> go run main.go
```

Starts proxy with loglevel=error, config filepath "some.json", with pprof enabled on port 6060.
```bash
cmd> go run -config some.json -loglevel 3 -pprof omain.go
```



