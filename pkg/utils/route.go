package utils

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procNetRoute is the kernel's IPv4 routing table.
const procNetRoute = "/proc/net/route"

// GetDefaultRouteDevice returns the name of the network interface that carries the
// IPv4 default route with the lowest metric.
func GetDefaultRouteDevice() (string, error) {
	return getDefaultRouteDevice(procNetRoute)
}

func getDefaultRouteDevice(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	var (
		device string
		best   int
	)

	scanner := bufio.NewScanner(f)
	// Skip the header line.
	scanner.Scan()
	for scanner.Scan() {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		// A default route has both destination and netmask set to 0.0.0.0.
		if fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil {
			continue
		}
		if device == "" || metric < best {
			device, best = fields[0], metric
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	if device == "" {
		return "", fmt.Errorf("no IPv4 default route found in %s", path)
	}

	return device, nil
}
