package ui

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/evilsocket/opensnitch/daemon/log"
	"github.com/evilsocket/opensnitch/daemon/procmon/monitor"
	"github.com/evilsocket/opensnitch/daemon/rule"
)

func (c *Client) getSocketPath(socketPath string) string {
	c.Lock()
	defer c.Unlock()

	if strings.HasPrefix(socketPath, "unix://") == true {
		c.isUnixSocket = true
		return socketPath[7:]
	}

	c.isUnixSocket = false
	return socketPath
}

func (c *Client) setSocketPath(socketPath string) {
	c.Lock()
	defer c.Unlock()

	c.socketPath = socketPath
}

func (c *Client) isProcMonitorEqual(newMonitorMethod string) bool {
	config.RLock()
	defer config.RUnlock()

	return newMonitorMethod == config.ProcMonitorMethod
}

func (c *Client) parseConf(rawConfig string) (conf Config, err error) {
	err = json.Unmarshal([]byte(rawConfig), &conf)
	return conf, err
}

func (c *Client) loadDiskConfiguration(reload bool) {
	raw, err := ioutil.ReadFile(configFile)
	if err != nil || len(raw) == 0 {
		// Sometimes we may receive 2 Write events on monitorConfigWorker,
		// Which may lead to read 0 bytes.
		log.Warning("Error loading configuration from disk %s: %s", configFile, err)
		return
	}

	if ok := c.loadConfiguration(raw); ok {
		if err := c.configWatcher.Add(configFile); err != nil {
			log.Error("Could not watch path: %s", err)
			return
		}
	}

	if reload {
		return
	}

	go c.monitorConfigWorker()
}

func (c *Client) loadConfiguration(rawConfig []byte) bool {
	config.Lock()
	defer config.Unlock()

	if err := json.Unmarshal(rawConfig, &config); err != nil {
		msg := fmt.Sprintf("Error parsing configuration %s: %s", configFile, err)
		log.Error(msg)
		c.SendWarningAlert(msg)
		return false
	}
	// firstly load config level, to detect further errors if any
	if config.LogLevel != nil {
		log.SetLogLevel(int(*config.LogLevel))
	}
	log.SetLogUTC(config.LogUTC)
	log.SetLogMicro(config.LogMicro)
	if config.Server.LogFile != "" {
		log.Close()
		log.OpenFile(config.Server.LogFile)
	}

	if config.Server.Address != "" {
		tempSocketPath := c.getSocketPath(config.Server.Address)
		if tempSocketPath != c.socketPath {
			// disconnect, and let the connection poller reconnect to the new address
			c.disconnect()
		}
		c.setSocketPath(tempSocketPath)
	}
	if config.DefaultAction != "" {
		clientDisconnectedRule.Action = rule.Action(config.DefaultAction)
		clientErrorRule.Action = rule.Action(config.DefaultAction)
	}
	if config.DefaultDuration != "" {
		clientDisconnectedRule.Duration = rule.Duration(config.DefaultDuration)
		clientErrorRule.Duration = rule.Duration(config.DefaultDuration)
	}
	if config.ProcMonitorMethod != "" {
		if err := monitor.ReconfigureMonitorMethod(config.ProcMonitorMethod); err != nil {
			msg := fmt.Sprintf("Unable to set new process monitor (%s) method from disk: %v", config.ProcMonitorMethod, err)
			log.Warning(msg)
			c.SendWarningAlert(msg)
		}
	}

	return true
}

func (c *Client) saveConfiguration(rawConfig string) (err error) {
	if _, err = c.parseConf(rawConfig); err != nil {
		return fmt.Errorf("Error parsing configuration %s: %s", rawConfig, err)
	}

	if err = os.Chmod(configFile, 0600); err != nil {
		log.Warning("unable to set permissions to default config: %s", err)
	}
	if err = ioutil.WriteFile(configFile, []byte(rawConfig), 0644); err != nil {
		log.Error("writing configuration to disk: %s", err)
		return err
	}
	return nil
}
