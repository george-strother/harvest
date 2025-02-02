package collectors

import (
	"fmt"
	"github.com/netapp/harvest/v2/cmd/poller/plugin"
	"github.com/netapp/harvest/v2/cmd/tools/rest"
	"github.com/netapp/harvest/v2/pkg/conf"
	"github.com/netapp/harvest/v2/pkg/logging"
	"github.com/netapp/harvest/v2/pkg/matrix"
	"github.com/netapp/harvest/v2/pkg/util"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	zapiValueKey = "environment-sensors-info.threshold-sensor-value"
	restValueKey = "value"
)

// CollectChassisFRU is here because both ZAPI and REST sensor.go plugin call it to collect
// `system chassis fru show`.
// Chassis FRU information is only available via private CLI
func collectChassisFRU(client *rest.Client, logger *logging.Logger) (map[string]int, error) {
	fields := []string{"fru-name", "type", "status", "connected-nodes", "num-nodes"}
	query := "api/private/cli/system/chassis/fru"
	filter := []string{"type=psu"}
	href := rest.NewHrefBuilder().
		APIPath(query).
		Fields(fields).
		Filter(filter).
		Build()

	result, err := rest.Fetch(client, href)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data href=%s err=%w", href, err)
	}

	// map of PSUs node -> numNode
	nodeToNumNode := make(map[string]int)

	for _, r := range result {
		cn := r.Get("connected_nodes")
		if !cn.Exists() {
			logger.Warn().
				Str("cluster", client.Cluster().Name).
				Str("fru", r.Get("fru_name").String()).
				Msg("fru has no connected nodes")
			continue
		}
		numNodes := int(r.Get("num_nodes").Int())
		for _, e := range cn.Array() {
			nodeToNumNode[e.String()] = numNodes
		}
	}
	return nodeToNumNode, nil
}

type sensorValue struct {
	node  string
	name  string
	value float64
	unit  string
}

type environmentMetric struct {
	key                   string
	ambientTemperature    []float64
	nonAmbientTemperature []float64
	fanSpeed              []float64
	powerSensor           map[string]*sensorValue
	voltageSensor         map[string]*sensorValue
	currentSensor         map[string]*sensorValue
}

var ambientRegex = regexp.MustCompile(`^(Ambient Temp|Ambient Temp \d|PSU\d AmbTemp|PSU\d Inlet|PSU\d Inlet Temp|In Flow Temp|Front Temp|Bat_Ambient \d|Riser Inlet Temp)$`)

var powerInRegex = regexp.MustCompile(`^PSU\d (InPwr Monitor|InPower|PIN|Power In)$`)

var voltageRegex = regexp.MustCompile(`^PSU\d (\d+V|InVoltage|VIN|AC In Volt)$`)

var CurrentRegex = regexp.MustCompile(`^PSU\d (\d+V Curr|Curr|InCurrent|Curr IIN|AC In Curr)$`)

var eMetrics = []string{
	"average_ambient_temperature",
	"average_fan_speed",
	"average_temperature",
	"max_fan_speed",
	"max_temperature",
	"min_ambient_temperature",
	"min_fan_speed",
	"min_temperature",
	"power",
}

func calculateEnvironmentMetrics(data *matrix.Matrix, logger *logging.Logger, valueKey string, myData *matrix.Matrix, nodeToNumNode map[string]int) ([]*matrix.Matrix, error) {
	sensorEnvironmentMetricMap := make(map[string]*environmentMetric)
	excludedSensors := make(map[string][]sensorValue)

	for k, instance := range data.GetInstances() {
		if !instance.IsExportable() {
			continue
		}
		iKey := instance.GetLabel("node")
		if iKey == "" {
			logger.Warn().Str("key", k).Msg("missing node label for instance")
			continue
		}
		sensorName := instance.GetLabel("sensor")
		if sensorName == "" {
			logger.Warn().Str("key", k).Msg("missing sensor name for instance")
			continue
		}
		if _, ok := sensorEnvironmentMetricMap[iKey]; !ok {
			sensorEnvironmentMetricMap[iKey] = &environmentMetric{key: iKey, ambientTemperature: []float64{}, nonAmbientTemperature: []float64{}, fanSpeed: []float64{}}
		}
		for mKey, metric := range data.GetMetrics() {
			if mKey != valueKey {
				continue
			}
			sensorType := instance.GetLabel("type")
			sensorUnit := instance.GetLabel("unit")

			isAmbientMatch := ambientRegex.MatchString(sensorName)
			isPowerMatch := powerInRegex.MatchString(sensorName)
			isVoltageMatch := voltageRegex.MatchString(sensorName)
			isCurrentMatch := CurrentRegex.MatchString(sensorName)

			logger.Trace().
				Bool("isAmbientMatch", isAmbientMatch).
				Bool("isPowerMatch", isPowerMatch).
				Bool("isVoltageMatch", isVoltageMatch).
				Bool("isCurrentMatch", isCurrentMatch).
				Str("sensorType", sensorType).
				Str("sensorUnit", sensorUnit).
				Str("sensorName", sensorName).
				Send()

			if sensorType == "thermal" && isAmbientMatch {
				if value, ok := metric.GetValueFloat64(instance); ok {
					sensorEnvironmentMetricMap[iKey].ambientTemperature = append(sensorEnvironmentMetricMap[iKey].ambientTemperature, value)
				}
			}

			if sensorType == "thermal" && !isAmbientMatch {
				// Exclude temperature sensors that contains sensor name `Margin` and value < 0
				value, ok := metric.GetValueFloat64(instance)
				if value > 0 && !strings.Contains(sensorName, "Margin") {
					if ok {
						sensorEnvironmentMetricMap[iKey].nonAmbientTemperature = append(sensorEnvironmentMetricMap[iKey].nonAmbientTemperature, value)
					}
				} else {
					excludedSensors[iKey] = append(excludedSensors[iKey], sensorValue{
						node:  iKey,
						name:  sensorName,
						value: value,
					})
				}
			}

			if sensorType == "fan" {
				if value, ok := metric.GetValueFloat64(instance); ok {
					sensorEnvironmentMetricMap[iKey].fanSpeed = append(sensorEnvironmentMetricMap[iKey].fanSpeed, value)
				}
			}

			if isPowerMatch {
				if value, ok := metric.GetValueFloat64(instance); ok {
					if !IsValidUnit(sensorUnit) {
						logger.Warn().Str("unit", sensorUnit).Float64("value", value).Msg("unknown power unit")
					} else {
						if sensorEnvironmentMetricMap[iKey].powerSensor == nil {
							sensorEnvironmentMetricMap[iKey].powerSensor = make(map[string]*sensorValue)
						}
						sensorEnvironmentMetricMap[iKey].powerSensor[k] = &sensorValue{
							node:  iKey,
							name:  sensorName,
							value: value,
							unit:  sensorUnit,
						}
					}
				}
			}

			if isVoltageMatch {
				if value, ok := metric.GetValueFloat64(instance); ok {
					if sensorEnvironmentMetricMap[iKey].voltageSensor == nil {
						sensorEnvironmentMetricMap[iKey].voltageSensor = make(map[string]*sensorValue)
					}
					sensorEnvironmentMetricMap[iKey].voltageSensor[k] = &sensorValue{
						node:  iKey,
						name:  sensorName,
						value: value,
						unit:  sensorUnit,
					}
				}
			}

			if isCurrentMatch {
				if value, ok := metric.GetValueFloat64(instance); ok {
					if sensorEnvironmentMetricMap[iKey].currentSensor == nil {
						sensorEnvironmentMetricMap[iKey].currentSensor = make(map[string]*sensorValue)
					}
					sensorEnvironmentMetricMap[iKey].currentSensor[k] = &sensorValue{
						node:  iKey,
						name:  sensorName,
						value: value,
						unit:  sensorUnit,
					}
				}
			}
		}
	}

	if len(excludedSensors) > 0 {
		var excludedSensorStr string
		for k, v := range excludedSensors {
			excludedSensorStr += " node:" + k + " sensor:" + fmt.Sprintf("%v", v)
		}
		logger.Logger.Info().Str("sensor", excludedSensorStr).
			Msg("sensor excluded")
	}

	whrSensors := make(map[string]*sensorValue)

	for key, v := range sensorEnvironmentMetricMap {
		instance, err2 := myData.NewInstance(key)
		if err2 != nil {
			logger.Logger.Warn().Str("key", key).Msg("instance not found")
			continue
		}
		// set node label
		instance.SetLabel("node", key)
		for _, k := range eMetrics {
			m := myData.GetMetric(k)
			switch k {
			case "power":
				var sumPower float64
				if len(v.powerSensor) > 0 {
					for _, v1 := range v.powerSensor {
						if v1.unit == "mW" || v1.unit == "mW*hr" {
							sumPower += v1.value / 1000
						} else if v1.unit == "W" || v1.unit == "W*hr" {
							sumPower += v1.value
						} else {
							logger.Logger.Warn().Str("node", key).Str("name", v1.name).Str("unit", v1.unit).Float64("value", v1.value).Msg("unknown power unit")
						}
						if v1.unit == "mW*hr" || v1.unit == "W*hr" {
							whrSensors[v1.name] = v1
						}
					}
				} else if len(v.voltageSensor) > 0 && len(v.voltageSensor) == len(v.currentSensor) {
					// sort voltage keys
					voltageKeys := make([]string, 0, len(v.voltageSensor))
					for k := range v.voltageSensor {
						voltageKeys = append(voltageKeys, k)
					}
					sort.Strings(voltageKeys)

					// sort current keys
					currentKeys := make([]string, 0, len(v.currentSensor))
					for k := range v.currentSensor {
						currentKeys = append(currentKeys, k)
					}
					sort.Strings(currentKeys)

					for i := range currentKeys {
						currentKey := currentKeys[i]
						voltageKey := voltageKeys[i]

						// get values
						currentSensorValue := v.currentSensor[currentKey]
						voltageSensorValue := v.voltageSensor[voltageKey]

						// convert units
						if currentSensorValue.unit == "mA" {
							currentSensorValue.value = currentSensorValue.value / 1000
						} else if currentSensorValue.unit != "A" {
							logger.Logger.Warn().Str("node", key).Str("unit", currentSensorValue.unit).Float64("value", currentSensorValue.value).Msg("unknown current unit")
						}

						if voltageSensorValue.unit == "mV" {
							voltageSensorValue.value = voltageSensorValue.value / 1000
						} else if voltageSensorValue.unit != "V" {
							logger.Logger.Warn().Str("node", key).Str("unit", voltageSensorValue.unit).Float64("value", voltageSensorValue.value).Msg("unknown voltage unit")
						}

						p := currentSensorValue.value * voltageSensorValue.value

						if !strings.EqualFold(voltageSensorValue.name, "in") && !strings.EqualFold(currentSensorValue.name, "in") {
							p = p / 0.93 // If the sensor names to do NOT contain "IN" or "in", then we need to adjust the power to account for loss in the power supply. We will use 0.93 as the power supply efficiency factor for all systems.
						}

						sumPower += p
					}
				} else {
					logger.Logger.Warn().Str("node", key).Int("current size", len(v.currentSensor)).Int("voltage size", len(v.voltageSensor)).Msg("current and voltage sensor are ignored")
				}

				numNode, ok := nodeToNumNode[key]
				if !ok {
					logger.Logger.Warn().Str("node", key).Msg("node not found in nodeToNumNode map")
					numNode = 1
				}
				sumPower = sumPower / float64(numNode)
				err2 = m.SetValueFloat64(instance, sumPower)
				if err2 != nil {
					logger.Logger.Error().Float64("power", sumPower).Err(err2).Msg("Unable to set power")
				}
			case "average_ambient_temperature":
				if len(v.ambientTemperature) > 0 {
					aaT := util.Avg(v.ambientTemperature)
					err2 = m.SetValueFloat64(instance, aaT)
					if err2 != nil {
						logger.Logger.Error().Float64("average_ambient_temperature", aaT).Err(err2).Msg("Unable to set average_ambient_temperature")
					}
				}
			case "min_ambient_temperature":
				maT := util.Min(v.ambientTemperature)
				err2 = m.SetValueFloat64(instance, maT)
				if err2 != nil {
					logger.Logger.Error().Float64("min_ambient_temperature", maT).Err(err2).Msg("Unable to set min_ambient_temperature")
				}
			case "max_temperature":
				mT := util.Max(v.nonAmbientTemperature)
				err2 = m.SetValueFloat64(instance, mT)
				if err2 != nil {
					logger.Logger.Error().Float64("max_temperature", mT).Err(err2).Msg("Unable to set max_temperature")
				}
			case "average_temperature":
				if len(v.nonAmbientTemperature) > 0 {
					nat := util.Avg(v.nonAmbientTemperature)
					err2 = m.SetValueFloat64(instance, nat)
					if err2 != nil {
						logger.Logger.Error().Float64("average_temperature", nat).Err(err2).Msg("Unable to set average_temperature")
					}
				}
			case "min_temperature":
				mT := util.Min(v.nonAmbientTemperature)
				err2 = m.SetValueFloat64(instance, mT)
				if err2 != nil {
					logger.Logger.Error().Float64("min_temperature", mT).Err(err2).Msg("Unable to set min_temperature")
				}
			case "average_fan_speed":
				if len(v.fanSpeed) > 0 {
					afs := util.Avg(v.fanSpeed)
					err2 = m.SetValueFloat64(instance, afs)
					if err2 != nil {
						logger.Logger.Error().Float64("average_fan_speed", afs).Err(err2).Msg("Unable to set average_fan_speed")
					}
				}
			case "max_fan_speed":
				mfs := util.Max(v.fanSpeed)
				err2 = m.SetValueFloat64(instance, mfs)
				if err2 != nil {
					logger.Logger.Error().Float64("max_fan_speed", mfs).Err(err2).Msg("Unable to set max_fan_speed")
				}
			case "min_fan_speed":
				mfs := util.Min(v.fanSpeed)
				err2 = m.SetValueFloat64(instance, mfs)
				if err2 != nil {
					logger.Logger.Error().Float64("min_fan_speed", mfs).Err(err2).Msg("Unable to set min_fan_speed")
				}
			}
		}
	}

	if len(whrSensors) > 0 {
		var whrSensorsStr string
		for _, v := range whrSensors {
			whrSensorsStr += " sensor:" + fmt.Sprintf("%v", *v)
		}
		logger.Logger.Info().Str("sensor", whrSensorsStr).
			Msg("sensor with *hr units")
	}

	return []*matrix.Matrix{myData}, nil
}

func NewSensor(p *plugin.AbstractPlugin) plugin.Plugin {
	return &Sensor{AbstractPlugin: p}
}

type Sensor struct {
	*plugin.AbstractPlugin
	data           *matrix.Matrix
	client         *rest.Client
	instanceKeys   map[string]string
	instanceLabels map[string]map[string]string
}

func (my *Sensor) Init() error {

	var err error
	if err := my.InitAbc(); err != nil {
		return err
	}

	timeout, _ := time.ParseDuration(rest.DefaultTimeout)
	if my.client, err = rest.New(conf.ZapiPoller(my.ParentParams), timeout, my.Auth); err != nil {
		my.Logger.Error().Err(err).Msg("connecting")
		return err
	}

	if err = my.client.Init(5); err != nil {
		return err
	}

	my.data = matrix.New(my.Parent+".Sensor", "environment_sensor", "environment_sensor")
	my.instanceKeys = make(map[string]string)
	my.instanceLabels = make(map[string]map[string]string)

	// init environment metrics in plugin matrix
	// create environment metric if not exists
	for _, k := range eMetrics {
		err := matrix.CreateMetric(k, my.data)
		if err != nil {
			my.Logger.Warn().Err(err).Str("key", k).Msg("error while creating metric")
		}
	}
	return nil
}

func (my *Sensor) Run(dataMap map[string]*matrix.Matrix) ([]*matrix.Matrix, error) {
	data := dataMap[my.Object]
	// Purge and reset data
	my.data.PurgeInstances()
	my.data.Reset()

	// Set all global labels if they don't already exist
	my.data.SetGlobalLabels(data.GetGlobalLabels())

	// Collect chassis fru show, so we can determine if a controller's PSUs are shared or not
	nodeToNumNode, err := collectChassisFRU(my.client, my.Logger)
	if err != nil {
		return nil, err
	}
	if len(nodeToNumNode) == 0 {
		my.Logger.Debug().Msg("No chassis field replaceable units found")
	}

	valueKey := zapiValueKey
	if my.Parent == "Rest" {
		valueKey = restValueKey
	}
	return calculateEnvironmentMetrics(data, my.Logger, valueKey, my.data, nodeToNumNode)
}
