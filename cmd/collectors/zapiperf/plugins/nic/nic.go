/*
 * Copyright NetApp Inc, 2021 All rights reserved

Package Description:
    Some postprocessing on counter data "nic_common"
      Converts link_speed to numeric MBs
      Adds custom metrics:
          - "rc_percent":    receive data utilization percent
          - "tx_percent":    sent data utilization percent
          - "util_percent":  max utilization percent
		  - "nic_state":     0 if port is up, 1 otherwise

*/

package nic

import (
	"github.com/netapp/harvest/v2/cmd/poller/plugin"
	"github.com/netapp/harvest/v2/pkg/errs"
	"github.com/netapp/harvest/v2/pkg/matrix"
	"math"
	"strconv"
	"strings"
)

type Nic struct {
	*plugin.AbstractPlugin
}

func New(p *plugin.AbstractPlugin) plugin.Plugin {
	return &Nic{AbstractPlugin: p}
}

// Run speed label is reported in bits-per-second and rx/tx is reported as bytes-per-second
func (n *Nic) Run(dataMap map[string]*matrix.Matrix) ([]*matrix.Matrix, error) {

	var read, write, rx, tx, util *matrix.Metric
	var err error

	data := dataMap[n.Object]
	if read = data.GetMetric("rx_bytes"); read == nil {
		return nil, errs.New(errs.ErrNoMetric, "rx_bytes")
	}

	if write = data.GetMetric("tx_bytes"); write == nil {
		return nil, errs.New(errs.ErrNoMetric, "tx_bytes")
	}

	if rx = data.GetMetric("rx_percent"); rx == nil {
		if rx, err = data.NewMetricFloat64("rx_percent"); err == nil {
			rx.SetProperty("raw")
		} else {
			return nil, err
		}

	}
	if tx = data.GetMetric("tx_percent"); tx == nil {
		if tx, err = data.NewMetricFloat64("tx_percent"); err == nil {
			tx.SetProperty("raw")
		} else {
			return nil, err
		}
	}

	if util = data.GetMetric("util_percent"); util == nil {
		if util, err = data.NewMetricFloat64("util_percent"); err == nil {
			util.SetProperty("raw")
		} else {
			return nil, err
		}
	}

	for _, instance := range data.GetInstances() {
		if !instance.IsExportable() {
			continue
		}

		var speed, base int
		var s string
		var err error

		s = instance.GetLabel("speed")

		if s != "" {
			if strings.HasSuffix(s, "M") {
				base, err = strconv.Atoi(strings.TrimSuffix(s, "M"))
				if err != nil {
					n.Logger.Warn().Msgf("convert speed [%s]", s)
				} else {
					// NIC speed value converted from Mbps to Bps(bytes per second)
					speed = base * 125000
					n.Logger.Trace().
						Str("originalSpeed", s).
						Int("convertedSpeedBps", speed).
						Msg("converted speed to Bps numeric")
				}
			} else if speed, err = strconv.Atoi(s); err != nil {
				n.Logger.Warn().Msgf("convert speed [%s]", s)
			}

			if speed != 0 {
				var rxBytes, txBytes, rxPercent, txPercent float64
				var rxOk, txOk bool

				if rxBytes, rxOk = read.GetValueFloat64(instance); rxOk {
					rxPercent = rxBytes / float64(speed)
					err := rx.SetValueFloat64(instance, rxPercent)
					if err != nil {
						n.Logger.Error().Stack().Err(err).Msg("error")
					}
				}

				if txBytes, txOk = write.GetValueFloat64(instance); txOk {
					txPercent = txBytes / float64(speed)
					err := tx.SetValueFloat64(instance, txPercent)
					if err != nil {
						n.Logger.Error().Stack().Err(err).Msg("error")
					}
				}

				if rxOk || txOk {
					err := util.SetValueFloat64(instance, math.Max(rxPercent, txPercent))
					if err != nil {
						n.Logger.Error().Stack().Err(err).Msg("error")
					}
				}
			}
		}

		if s = instance.GetLabel("speed"); strings.HasSuffix(s, "M") {
			base, err = strconv.Atoi(strings.TrimSuffix(s, "M"))
			if err != nil {
				n.Logger.Warn().Msgf("convert speed [%s]", s)
			} else {
				// NIC speed value converted from Mbps to bps(bits per second)
				speed = base * 1_000_000
				instance.SetLabel("speed", strconv.Itoa(speed))
				n.Logger.Trace().
					Str("originalSpeed", s).
					Int("convertedSpeedbps", speed).
					Msg("converted speed to bps numeric")
			}
		}

		// truncate redundant prefix in nic type
		if t := instance.GetLabel("type"); strings.HasPrefix(t, "nic_") {
			instance.SetLabel("type", strings.TrimPrefix(t, "nic_"))
		}

	}

	return nil, nil
}
