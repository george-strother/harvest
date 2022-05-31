/*
 * Copyright NetApp Inc, 2021 All rights reserved
 */

package volume

import (
	"github.com/netapp/harvest/v2/cmd/poller/plugin"
	"github.com/netapp/harvest/v2/pkg/matrix"
	"regexp"
	"strings"
)

type Volume struct {
	*plugin.AbstractPlugin
}

func New(p *plugin.AbstractPlugin) plugin.Plugin {
	return &Volume{AbstractPlugin: p}
}

//@TODO cleanup logging
//@TODO rewrite using vector arithmetic
// will simplify the code a whole!!!

func (me *Volume) Run(data *matrix.Matrix) ([]*matrix.Matrix, error) {

	re := regexp.MustCompile(`^(.*)__(\d{4})$`)

	cache := data.Clone(false, true, false)
	cache.UUID += ".Volume"

	// create flexgroup instance cache
	for _, i := range data.GetInstances() {
		if match := re.FindStringSubmatch(i.GetLabel("volume")); len(match) == 3 {
			// instance key is svm.flexgroup-volume
			key := i.GetLabel("svm") + "." + match[1]
			if cache.GetInstance(key) == nil {
				fg, _ := cache.NewInstance(key)
				fg.SetLabels(i.GetLabels().Copy())
				fg.SetLabel("volume", match[1])
				fg.SetLabel("style", "flexgroup")
			}
			i.SetLabel("style", "flexgroup_constituent")
			i.SetExportable(false)
		} else {
			i.SetLabel("style", "flexvol")
		}
	}

	me.Logger.Debug().Msgf("extracted %d flexgroup volumes", len(cache.GetInstances()))

	//cache.Reset()

	// create summary
	for _, i := range data.GetInstances() {
		if match := re.FindStringSubmatch(i.GetLabel("volume")); len(match) == 3 {
			// instance key is svm.flexgroup-volume
			key := i.GetLabel("svm") + "." + match[1]
			fg := cache.GetInstance(key)
			if fg == nil {
				me.Logger.Error().Stack().Err(nil).Msgf("instance [%s] not in local cache", key)
				continue
			}

			for mkey, m := range data.GetMetrics() {

				if !m.IsExportable() && m.GetType() != "float64" {
					continue
				}

				fgm := cache.GetMetric(mkey)
				if fgm == nil {
					me.Logger.Error().Stack().Err(nil).Msgf("metric [%s] not in local cache", mkey)
					continue
				}

				me.Logger.Trace().Msgf("(%s) handling metric (%s)", fg.GetLabel("volume"), mkey)

				if value, ok := m.GetValueFloat64(i); ok {

					fgv, _ := fgm.GetValueFloat64(fg)

					// non-latency metrics: simple sum
					if !strings.HasSuffix(m.GetName(), "_latency") {

						err := fgm.SetValueFloat64(fg, fgv+value)
						if err != nil {
							me.Logger.Error().Stack().Err(err).Msg("error")
						}
						// just for debugging
						fgv2, _ := fgm.GetValueFloat64(fg)

						me.Logger.Trace().Msgf("   > simple increment %f + %f = %f", fgv, value, fgv2)
						continue
					}

					// latency metric: weighted sum
					opsKey := ""
					if strings.Contains(mkey, "_latency") {
						opsKey = m.GetComment()
					}
					me.Logger.Trace().Msgf("    > weighted increment <%s * %s>", mkey, opsKey)

					if ops := data.GetMetric(opsKey); ops != nil {
						if opsValue, ok := ops.GetValueFloat64(i); ok {

							prod := value * opsValue
							err := fgm.SetValueFloat64(fg, fgv+prod)
							if err != nil {
								me.Logger.Error().Stack().Err(err).Msg("error")
							}

							// debugging
							fgv2, _ := fgm.GetValueFloat64(fg)

							me.Logger.Trace().Msgf("       %f + (%f * %f) (=%f) = %f", fgv, value, opsValue, prod, fgv2)
						} else {
							me.Logger.Trace().Msg("       no ops value SKIP")
						}
					}

				}

			}
		}
	}

	// normalize latency values
	for _, i := range cache.GetInstances() {
		for mkey, m := range cache.GetMetrics() {
			if m.IsExportable() && strings.HasSuffix(m.GetName(), "_latency") {

				if value, ok := m.GetValueFloat64(i); ok {

					opsKey := ""
					if strings.Contains(mkey, "_latency") {
						opsKey = m.GetComment()
					}

					if ops := cache.GetMetric(opsKey); ops != nil {

						if opsValue, ok := ops.GetValueFloat64(i); ok && opsValue != 0 {
							err := m.SetValueFloat64(i, value/opsValue)
							if err != nil {
								me.Logger.Error().Stack().Err(err).Msgf("error")
							}
						} else {
							m.SetValueNAN(i)
						}
					}
				}

			}
		}
	}

	return []*matrix.Matrix{cache}, nil
}
