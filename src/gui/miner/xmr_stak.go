package miner

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// XmrStak implements the miner interface for the xmr-stak miner
// https://github.com/fireice-uk/xmr-stak
type XmrStak struct {
	Base
	name             string
	endpoint         string
	lastHashrate     float64
	resultStatsCache XmrStakResponse
}

// XmrStakResponse contains the data from xmr-stak API
// Generated with https://mholt.github.io/json-to-go/
type XmrStakResponse struct {
	Version  string `json:"version"`
	Hashrate struct {
		Threads [][]interface{} `json:"threads"`
		Total   []float64       `json:"total"`
		Highest float64         `json:"highest"`
	} `json:"hashrate"`
	Results struct {
		DiffCurrent int     `json:"diff_current"`
		SharesGood  int     `json:"shares_good"`
		SharesTotal int     `json:"shares_total"`
		AvgTime     float64 `json:"avg_time"`
		HashesTotal int     `json:"hashes_total"`
		Best        []int   `json:"best"`
		ErrorLog    []struct {
			Count    int    `json:"count"`
			LastSeen int    `json:"last_seen"`
			Text     string `json:"text"`
		} `json:"error_log"`
	} `json:"results"`
	Connection struct {
		Pool     string `json:"pool"`
		Uptime   int    `json:"uptime"`
		Ping     int    `json:"ping"`
		ErrorLog []struct {
			LastSeen int    `json:"last_seen"`
			Text     string `json:"text"`
		} `json:"error_log"`
	} `json:"connection"`
}

// NewXmrStak creates a new xmr-stak miner instance
func NewXmrStak(config Config) (*XmrStak, error) {

	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:16000/api.json"
	}

	miner := XmrStak{
		// We've switched to our own miner in V4, xmr-stak, but I'm keeping
		// everything else xmr-stak for clarity
		name:     "xmr-stak",
		endpoint: endpoint,
	}
	miner.Base.executableName = filepath.Base(config.Path)
	miner.Base.executablePath = filepath.Dir(config.Path)

	return &miner, nil
}

// WriteConfig writes the miner's configuration in the xmr-stak format
func (miner *XmrStak) WriteConfig(
	poolEndpoint string,
	walletAddress string,
	processingConfig ProcessingConfig) error {

	// For xmr-stak, we assume some values for now. I'll extend this in
	// future releases, for now it's fine
	err := ioutil.WriteFile(
		filepath.Join(miner.Base.executablePath, "config.txt"),
		[]byte(miner.defaultConfig()),
		0644)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(
		filepath.Join(miner.Base.executablePath, "pools.txt"),
		[]byte(miner.buildPoolConfig(poolEndpoint, walletAddress)),
		0644)
	if err != nil {
		return err
	}

	// With xmr-stak you have the option to disable CPU mining, in that case
	// we can't just check for 0. If the cpu.txt file exists then a zero
	// means we're disabling it, else this is firstrun
	_, err = os.Stat(filepath.Join(miner.Base.executablePath, "cpu.txt"))
	if err == nil {
		// File exists, we may disable
		err = ioutil.WriteFile(
			filepath.Join(miner.Base.executablePath, "cpu.txt"),
			[]byte(miner.cpuConfig(processingConfig.Threads)),
			0644)
		if err != nil {
			return err
		}
	}
	// Reset hashrate
	miner.lastHashrate = 0.00
	return nil
}

// GetProcessingConfig returns the current miner processing config
// TODO: Currently only CPU threads, extend this to full CPU/GPU config
func (miner *XmrStak) GetProcessingConfig() ProcessingConfig {
	return ProcessingConfig{
		MaxUsage: 0,
		// xmr-stak reports GPU + CPU threads in the same section, for that reason
		// we need to check the actual cpu.txt file to get the real thread count
		Threads:    miner.getCPUThreadcount(),
		MaxThreads: uint16(runtime.NumCPU()),
		Type:       miner.name,
	}
}

// GetName returns the name of the miner
func (miner *XmrStak) GetName() string {
	return miner.name
}

// GetLastHashrate returns the last reported hashrate
func (miner *XmrStak) GetLastHashrate() float64 {
	return miner.lastHashrate
}

// getCPUThreadcount returns the threads used for the CPU as read from the
// config
func (miner *XmrStak) getCPUThreadcount() uint16 {
	configPath := filepath.Join(miner.Base.executablePath, "cpu.txt")
	configFileBytes, err := ioutil.ReadFile(configPath)
	// If config file doesn't exist, return 0 as the threadcount
	if err != nil {
		return 0
	}
	// xmr-stak uses a strange JSON-like format, I haven't found a Go library
	// that can parse the file, so we're doing some basic string matches
	lines := strings.Split(string(configFileBytes), "\n")
	var validLines string
	for _, line := range lines {
		for _, char := range line {
			// This is a very very very basic check if this line is actually a comment
			if string(char) == "/" || string(char) == "*" {
				// Skip this line
				break
			} else {
				validLines += string(char)
			}
		}
	}

	var threadcount uint16
	// Match anything enclosed in {} for JSON object
	var re = regexp.MustCompile(`{*}`)
	for _ = range re.FindAllString(validLines, -1) {
		threadcount++
	}
	return threadcount
}

// GetStats returns the current miner stats
func (miner *XmrStak) GetStats() (Stats, error) {
	var stats Stats
	var xmrStats XmrStakResponse
	resp, err := http.Get(miner.endpoint)
	if err != nil {
		return stats, err
	}
	err = json.NewDecoder(resp.Body).Decode(&xmrStats)
	if err != nil {
		return stats, err
	}

	var hashrate float64
	if len(xmrStats.Hashrate.Total) > 0 {
		hashrate = xmrStats.Hashrate.Total[0]
	}
	miner.lastHashrate = hashrate

	var errors []string
	if len(xmrStats.Connection.ErrorLog) > 0 {
		for _, err := range xmrStats.Connection.ErrorLog {
			errors = append(errors, fmt.Sprintf("%s",
				err.Text,
			))
		}
	}
	if len(xmrStats.Results.ErrorLog) > 0 {
		for _, err := range xmrStats.Results.ErrorLog {
			errors = append(errors, fmt.Sprintf("(%d) %s",
				err.Count,
				err.Text,
			))
		}
	}

	stats = Stats{
		Hashrate:          hashrate,
		HashrateHuman:     HumanizeHashrate(hashrate),
		CurrentDifficulty: xmrStats.Results.DiffCurrent,
		Uptime:            xmrStats.Connection.Uptime,
		UptimeHuman:       HumanizeTime(xmrStats.Connection.Uptime),
		SharesGood:        xmrStats.Results.SharesGood,
		SharesBad:         xmrStats.Results.SharesTotal - xmrStats.Results.SharesGood,
		Errors:            errors,
	}
	miner.resultStatsCache = xmrStats
	return stats, nil
}

// defaultConfig returns the base xmr-stak config
// xmr-stak uses a JSON format that doesn't have a compatible Go
// parser which is why I'm doing this as text or templates
func (miner *XmrStak) defaultConfig() string {
	return `

	/*
 * Network timeouts.
 * Because of the way this client is written it doesn't need to constantly talk (keep-alive) to the server to make
 * sure it is there. We detect a buggy / overloaded server by the call timeout. The default values will be ok for
 * nearly all cases. If they aren't the pool has most likely overload issues. Low call timeout values are preferable -
 * long timeouts mean that we waste hashes on potentially stale jobs. Connection report will tell you how long the
 * server usually takes to process our calls.
 *
 * call_timeout - How long should we wait for a response from the server before we assume it is dead and drop the connection.
 * retry_time	- How long should we wait before another connection attempt.
 *                Both values are in seconds.
 * giveup_limit - Limit how many times we try to reconnect to the pool. Zero means no limit. Note that stak miners
 *                don't mine while the connection is lost, so your computer's power usage goes down to idle.
 */
"call_timeout" : 10,
"retry_time" : 30,
"giveup_limit" : 0,

/*
 * Output control.
 * Since most people are used to miners printing all the time, that's what we do by default too. This is suboptimal
 * really, since you cannot see errors under pages and pages of text and performance stats. Given that we have internal
 * performance monitors, there is very little reason to spew out pages of text instead of concise reports.
 * Press 'h' (hashrate), 'r' (results) or 'c' (connection) to print reports.
 *
 * verbose_level - 0 - Don't print anything.
 *                 1 - Print intro, connection event, disconnect event
 *                 2 - All of level 1, and new job (block) event if the difficulty is different from the last job
 *                 3 - All of level 1, and new job (block) event in all cases, result submission event.
 *                 4 - All of level 3, and automatic hashrate report printing
 *
 * print_motd    - Display messages from your pool operator in the hashrate result.
 */
"verbose_level" : 3,
"print_motd" : true,

/*
 * Automatic hashrate report
 *
 * h_print_time - How often, in seconds, should we print a hashrate report if verbose_level is set to 4.
 *                This option has no effect if verbose_level is not 4.
 */
"h_print_time" : 60,

/*
 * Manual hardware AES override
 *
 * Some VMs don't report AES capability correctly. You can set this value to true to enforce hardware AES or
 * to false to force disable AES or null to let the miner decide if AES is used.
 *
 * WARNING: setting this to true on a CPU that doesn't support hardware AES will crash the miner.
 */
"aes_override" : null,

/*
 * LARGE PAGE SUPPORT
 * Large pages need a properly set up OS. It can be difficult if you are not used to systems administration,
 * but the performance results are worth the trouble - you will get around 20% boost. Slow memory mode is
 * meant as a backup, you won't get stellar results there. If you are running into trouble, especially
 * on Windows, please read the common issues in the README and FAQ.
 *
 * On Linux you will need to configure large page support and increase your memlock limit (ulimit -l).
 *
 * To set large page support, add the following to "/etc/sysctl.d/60-hugepages.conf":
 *     vm.nr_hugepages=128
 * You WILL need to run "sudo sysctl --system" for these settings to take effect on your system (or reboot).
 *  In some cases (many threads, very large CPU, etc) you may need more than 128
 *   (try 256 if there are still complaints from thread inits)
 *
 * To increase the memlock (ulimit -l), add following lines to /etc/security/limits.d/60-memlock.conf:
 *     *    - memlock 262144
 *     root - memlock 262144
 * You WILL need to log out and log back in for these settings to take effect on your user (no need to reboot, just relogin in your session).
 *
 * Check with "/sbin/sysctl vm.nr_hugepages ; ulimit -l" to validate
 *
 * Memory locking means that the kernel can't swap out the page to disk - something that is unlikely to happen on a
 * command line system that isn't starved of memory. I haven't observed any difference on a CLI Linux system between
 * locked and unlocked memory. If that is your setup see option "no_mlck".
 *
 *
 * use_slow_memory defines our behaviour with regards to large pages. There are three possible options here:
 * always  - Don't even try to use large pages. Always use slow memory.
 * warn    - We will try to use large pages, but fall back to slow memory if that fails.
 * no_mlck - This option is only relevant on Linux, where we can use large pages without locking memory.
 *           It will never use slow memory, but it won't attempt to mlock
 * never   - If we fail to allocate large pages we will print an error and exit.
 */
"use_slow_memory" : "warn",

/*
 * TLS Settings
 * If you need real security, make sure tls_secure_algo is enabled (otherwise MITM attack can downgrade encryption
 * to trivially breakable stuff like DES and MD5), and verify the server's fingerprint through a trusted channel.
 *
 * tls_secure_algo - Use only secure algorithms. This will make us quit with an error if we can't negotiate a secure algo.
 */
"tls_secure_algo" : true,

/*
 * Daemon mode
 *
 * If you are running the process in the background and you don't need the keyboard reports, set this to true.
 * This should solve the hashrate problems on some emulated terminals.
 */
"daemon_mode" : true,

/*
 * Output file
 *
 * output_file  - This option will log all output to a file.
 *
 */
"output_file" : "",

/*
 * Built-in web server
 * I like checking my hashrate on my phone. Don't you?
 * Keep in mind that you will need to set up port forwarding on your router if you want to access it from
 * outside of your home network. Ports lower than 1024 on Linux systems will require root.
 *
 * httpd_port - Port we should listen on. Default, 0, will switch off the server.
 */
"httpd_port" : 16000,

/*
 * HTTP Authentication
 *
 * This allows you to set a password to keep people on the Internet from snooping on your hashrate.
 * Keep in mind that this is based on HTTP Digest, which is based on MD5. To a determined attacker
 * who is able to read your traffic it is as easy to break a bog door latch.
 *
 * http_login - Login. Empty login disables authentication.
 * http_pass  - Password.
 */
"http_login" : "",
"http_pass" : "",

/*
 * prefer_ipv4 - IPv6 preference. If the host is available on both IPv4 and IPv6 net, which one should be choose?
 *               This setting will only be needed in 2020's. No need to worry about it now.
 */
"prefer_ipv4" : true,
	`
}

// buildPoolConfig returns the XmrStak pool config to be written to file
// xmr-stak uses a JSON format that doesn't have a compatible Go
// parser which is why I'm doing this as text or templates
func (miner *XmrStak) buildPoolConfig(
	poolEndpoint string,
	walletAddress string) string {

	return `
"pool_list" :
[
	{"pool_address" : "` + poolEndpoint + `", "wallet_address" : "` + walletAddress + `", "rig_id" : "", "pool_password" : "Torque GUI Miner", "use_nicehash" : false, "use_tls" : false, "tls_fingerprint" : "", "pool_weight" : 1 },
],
"currency" : "klaro",
		`
}

// cpuConfig returns the XmrStak CPU config to be written to file based on
// the amount of threads
// xmr-stak uses a JSON format that doesn't have a compatible Go
// parser which is why I'm doing this as text or templates
func (miner *XmrStak) cpuConfig(threads uint16) string {

	var threadsConfig string
	for i := uint16(0); i < threads; i++ {
		threadsConfig += fmt.Sprintf("{ \"low_power_mode\" : false, \"no_prefetch\" : true, \"asm\" : \"auto\", \"affine_to_cpu\" : %d },", i)
	}

	return `
	/*
	 * Thread configuration for each thread. Make sure it matches the number above.
	 * low_power_mode - This can either be a boolean (true or false), or a number between 1 to 5. When set to true,
	 *                  this mode will double the cache usage, and double the single thread performance. It will
	 *                  consume much less power (as less cores are working), but will max out at around 80-85% of
	 *                  the maximum performance. When set to a number N greater than 1, this mode will increase the
	 *                  cache usage and single thread performance by N times.
	 *
	 * no_prefetch    - Some systems can gain up to extra 5% here, but sometimes it will have no difference or make
	 *                  things slower.
	 *
	 * asm            - Allow to switch to a assembler version of cryptonight_v8; allowed value [auto, off, intel_avx, amd_avx]
	 *                    - auto: xmr-stak will automatically detect the asm type (default)
	 *                    - off: disable the usage of optimized assembler
	 *                    - intel_avx: supports Intel cpus with avx instructions e.g. Xeon v2, Core i7/i5/i3 3xxx, Pentium G2xxx, Celeron G1xxx
	 *                    - amd_avx: supports AMD cpus with avx instructions e.g. AMD Ryzen 1xxx and 2xxx series
	 *
	 * affine_to_cpu  - This can be either false (no affinity), or the CPU core number. Note that on hyperthreading
	 *                  systems it is better to assign threads to physical cores. On Windows this usually means selecting
	 *                  even or odd numbered cpu numbers. For Linux it will be usually the lower CPU numbers, so for a 4
	 *                  physical core CPU you should select cpu numbers 0-3.
	 *
	 * On the first run the miner will look at your system and suggest a basic configuration that will work,
	 * you can try to tweak it from there to get the best performance.
	 *
	 * A filled out configuration should look like this:
	 * "cpu_threads_conf" :
	 * [
	 *      { "low_power_mode" : false, "no_prefetch" : true, "asm" : "auto", "affine_to_cpu" : 0 },
	 *      { "low_power_mode" : false, "no_prefetch" : true, "asm" : "auto", "affine_to_cpu" : 1 },
	 * ],
	 * If you do not wish to mine with your CPU(s) then use:
	 * "cpu_threads_conf" :
	 * null,
	 */

	"cpu_threads_conf" :
	[
` + threadsConfig + `
	],
`
}
