package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/howeyc/fsnotify"
)

var (
	// Flag options
	auto        = flag.Bool("auto", false, "re-generates the site when files are modified")
	baseurl     = flag.String("base-url", "", "serves the website from a given base url")
	source      = flag.String("source", "", "directory where jkl will look to transform files")
	destination = flag.String("destination", "_site", "directory where jkl will write files to")
	server      = flag.Bool("server", false, "fires up a server that will host your _site directory if True")
	port        = flag.String("server_port", ":4000", "the port that the jkl server will run on")
	deploy      = flag.Bool("s3", false, "deploys the website to S3")
	s3key       = flag.String("s3_key", "", "s3 access key")
	s3secret    = flag.String("s3_secret", "", "s3 secret key")
	s3bucket    = flag.String("s3_bucket", "", "s3 bucket name")
	verbose     = flag.Bool("verbose", false, "runs jkl with verbose output if True")

	// Chnge the default output of the -help flag.
	usage = func() {
		fmt.Println("Usage: jkl [OPTION]... [SOURCE]")
		flag.PrintDefaults()
		fmt.Println(`
	Examples:
	  jkl                 generates site from current working directory
	  jkl --server        generates site and serves at localhost:4000
	  jkl /path/to/site   generates site from source dir /path/to/site

	Report bugs to <https://github.com/bradrydzewski/jkl/issues>
	jkl home page: <https://github.com/bradrydzewski/jkl>`)
		os.Exit(2)
	}
)

// Mutex used when doing auto-builds
var mu sync.RWMutex

func main() {

	// Parse the input parameters
	flag.Usage = usage
	flag.Parse()

	// User may specify the source as a non-flag variable
	if flag.NArg() > 0 {
		source = &flag.Args()[0]
	}

	// Convert the directory to an absolute path
	src, _ := filepath.Abs(*source)
	dest, _ := filepath.Abs(*destination)

	// Change the working directory to the website's source directory
	os.Chdir(src)

	// Initialize the jkl website
	site, err := NewSite(src, dest)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Set any site variables that were overriden / provided in the cli args
	if *baseurl != "" || site.Conf.Get("baseurl") == nil {
		site.Conf.Set("baseurl", *baseurl)
	}

	// Generate the static website
	if err := site.Generate(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Deploys the static website to S3
	if *deploy {

		var conf *DeployConfig
		// Read the S3 configuration details if not provided as
		// command line
		if *s3key == "" {
			path := filepath.Join(site.Src, "_jkl_s3.yml")
			conf, err = ParseDeployConfig(path)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		} else {
			// else use the command line args
			conf = &DeployConfig{*s3key, *s3secret, *s3bucket}
		}

		if err := site.Deploy(conf.Key, conf.Secret, conf.Bucket); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	// If the auto option is enabled, use fsnotify to watch
	// and re-generate the site if files change.
	if *auto {
		fmt.Printf("Listening for changes to %s\n", site.Src)
		go watch(site)
	}

	// If the server option is enabled, launch a webserver
	if *server {

		// Change the working directory to the _site directory
		os.Chdir(dest)

		// Create the handler to serve from the filesystem
		handler := func(w http.ResponseWriter, r *http.Request) {
			mu.RLock()
			defer mu.RUnlock()

			base := site.Conf.GetString("baseurl")
			path := r.URL.Path
			pathList := filepath.SplitList(path)
			if len(pathList) > 0 && pathList[0] == base {
				path = strings.Join(pathList[len(pathList):], "/")
			}

			path = filepath.Clean(path)
			path = filepath.Join(dest, path)
			http.ServeFile(w, r, path)
		}
		http.HandleFunc("/", handler)

		// Serve the website from the _site directory
		fmt.Printf("Starting server on port %s\n", *port)
		if err := http.ListenAndServe(*port, nil); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	os.Exit(0)
}

func watch(site *Site) {

	// Setup the inotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Println(err)
		return
	}

	// Get recursive list of directories to watch
	for _, path := range dirs(site.Src) {
		if err := watcher.Watch(path); err != nil {
			fmt.Println(err)
			return
		}
	}

	for {
		select {
		case ev := <-watcher.Event:
			// Ignore changes to the _site directoy, hidden, or temp files
			if !strings.HasPrefix(ev.Name, site.Dest) && !isHiddenOrTemp(ev.Name) {
				fmt.Println("Event: ", ev.String())
				recompile(site)
			}
		case err := <-watcher.Error:
			fmt.Println("inotify error:", err)
		}
	}
}

func recompile(site *Site) {
	mu.Lock()
	defer mu.Unlock()

	if err := site.Reload(); err != nil {
		fmt.Println(err)
		return
	}

	if err := site.Generate(); err != nil {
		fmt.Println(err)
		return
	}
}

func logf(msg string, args ...interface{}) {
	if *verbose {
		println(fmt.Sprintf(msg, args...))
	}
}
