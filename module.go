// Package capturecontrol is an example capture-control sensor that drives
// the data manager's capture overrides and sequence lifecycle via DoCommand.
//
// The data manager polls Readings() and looks for two keys:
//
//   - "overrides": []CaptureConfigReading — per resource/method, sets
//     capture_frequency_hz (0 disables) and tags. See
//     services/datamanager/data_manager.go for the schema.
//   - "sequences": []SequenceReading — each entry opens a sequence on first
//     appearance and closes it when it disappears. An empty list closes all
//     open sequences.
package capturecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sensor "go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var (
	CaptureControlSensor = resource.NewModel("viam", "capture-control", "capture-control-sensor")

	errNoResources = errors.New("at least one entry in 'resources' is required")
)

func init() {
	resource.RegisterComponent(sensor.API, CaptureControlSensor,
		resource.Registration[sensor.Sensor, *Config]{
			Constructor: newCaptureControlSensor,
		},
	)
}

type ResourceMethod struct {
	ResourceName string `json:"resource_name"`
	Method       string `json:"method"`
}

type Config struct {
	// Resources are the resource/method pairs this sensor controls. Each pair
	// is emitted under "overrides" every poll, and under "sequences" when a
	// sequence is open.
	Resources []ResourceMethod `json:"resources"`

	// DefaultCaptureFrequencyHz is the capture frequency emitted until a
	// start_capture DoCommand changes it. 0 disables capture.
	DefaultCaptureFrequencyHz float32 `json:"default_capture_frequency_hz"`

	// DefaultTags are the tags emitted on "overrides" until a start_capture
	// DoCommand changes them.
	DefaultTags []string `json:"default_tags,omitempty"`
}

func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if len(cfg.Resources) == 0 {
		return nil, nil, fmt.Errorf("%s: %w", path, errNoResources)
	}
	for i, r := range cfg.Resources {
		if r.ResourceName == "" {
			return nil, nil, fmt.Errorf("%s.resources[%d].resource_name is required", path, i)
		}
		if r.Method == "" {
			return nil, nil, fmt.Errorf("%s.resources[%d].method is required", path, i)
		}
	}
	if cfg.DefaultCaptureFrequencyHz < 0 {
		return nil, nil, fmt.Errorf("%s.default_capture_frequency_hz must be >= 0", path)
	}
	return nil, nil, nil
}

type captureControlSensor struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *Config

	// sequenceResources is a pre-built []{resource_name, method} slice
	// reused every Readings() call while a sequence is active.
	sequenceResources []interface{}

	mu                 sync.Mutex
	captureFrequencyHz float32
	captureTags        []string
	sequenceActive     bool
	sequenceTags       []string
}

func newCaptureControlSensor(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (sensor.Sensor, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}
	return NewCaptureControlSensor(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func NewCaptureControlSensor(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (sensor.Sensor, error) {
	sequenceResources := make([]interface{}, 0, len(conf.Resources))
	for _, r := range conf.Resources {
		sequenceResources = append(sequenceResources, map[string]interface{}{
			"resource_name": r.ResourceName,
			"method":        r.Method,
		})
	}
	return &captureControlSensor{
		name:               name,
		logger:             logger,
		cfg:                conf,
		sequenceResources:  sequenceResources,
		captureFrequencyHz: conf.DefaultCaptureFrequencyHz,
		captureTags:        conf.DefaultTags,
	}, nil
}

func (s *captureControlSensor) Name() resource.Name {
	return s.name
}

func (s *captureControlSensor) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	s.mu.Lock()
	freq := s.captureFrequencyHz
	captureTags := toAny(s.captureTags)
	seqActive := s.sequenceActive
	seqTags := toAny(s.sequenceTags)
	s.mu.Unlock()

	overrides := make([]interface{}, 0, len(s.cfg.Resources))
	for _, r := range s.cfg.Resources {
		override := map[string]interface{}{
			"resource_name":        r.ResourceName,
			"method":               r.Method,
			"capture_frequency_hz": freq,
		}
		if len(captureTags) > 0 {
			override["tags"] = captureTags
		}
		overrides = append(overrides, override)
	}

	sequences := []interface{}{}
	if seqActive {
		sequences = append(sequences, map[string]interface{}{
			"resources":     s.sequenceResources,
			"sequence_tags": seqTags,
		})
	}

	return map[string]interface{}{
		"overrides": overrides,
		"sequences": sequences,
	}, nil
}

var commandKeys = []string{"start_capture", "stop_capture", "start_sequence", "stop_sequence"}

// DoCommand exposes four commands. Each is selected by setting its key to true;
// optional arguments are read from sibling keys on the same map. Exactly one
// command key may be true per call.
//
//	{"start_capture": true, "frequency_hz": 2.0, "tags": ["run-1"]}  // also opens a sequence
//	{"stop_capture":  true}                                          // also closes the sequence
//	{"start_sequence": true, "tags": ["run-1"]}
//	{"stop_sequence":  true}
func (s *captureControlSensor) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	selected := ""
	for _, k := range commandKeys {
		if cmd[k] != true {
			continue
		}
		if selected != "" {
			return nil, fmt.Errorf("only one command may be set per call; got %q and %q", selected, k)
		}
		selected = k
	}

	switch selected {
	case "start_capture":
		freq := s.cfg.DefaultCaptureFrequencyHz
		if raw, present := cmd["frequency_hz"]; present {
			v, ok := raw.(float64)
			if !ok {
				return nil, fmt.Errorf("frequency_hz must be a number")
			}
			if v < 0 {
				return nil, fmt.Errorf("frequency_hz must be >= 0")
			}
			freq = float32(v)
		}
		tags := s.cfg.DefaultTags
		if raw, present := cmd["tags"]; present {
			v, ok := stringList(raw)
			if !ok {
				return nil, fmt.Errorf("tags must be an array of strings")
			}
			tags = v
		}
		s.mu.Lock()
		s.captureFrequencyHz = freq
		s.captureTags = tags
		s.sequenceActive = true
		s.sequenceTags = tags
		s.mu.Unlock()
		s.logger.Infof("start_capture frequency_hz=%v tags=%v (sequence opened)", freq, tags)
		tagsAny := toAny(tags)
		return map[string]interface{}{
			"capture_frequency_hz": freq,
			"tags":                 tagsAny,
			"sequence_tags":        tagsAny,
		}, nil

	case "stop_capture":
		s.mu.Lock()
		s.captureFrequencyHz = 0
		s.sequenceActive = false
		s.sequenceTags = nil
		s.mu.Unlock()
		s.logger.Info("stop_capture (sequence closed)")
		return nil, nil

	case "start_sequence":
		var tags []string
		if raw, present := cmd["tags"]; present {
			v, ok := stringList(raw)
			if !ok {
				return nil, fmt.Errorf("tags must be an array of strings")
			}
			tags = v
		}
		s.mu.Lock()
		s.sequenceActive = true
		s.sequenceTags = tags
		s.mu.Unlock()
		s.logger.Infof("start_sequence tags=%v", tags)
		return map[string]interface{}{"sequence_tags": toAny(tags)}, nil

	case "stop_sequence":
		s.mu.Lock()
		s.sequenceActive = false
		s.sequenceTags = nil
		s.mu.Unlock()
		s.logger.Info("stop_sequence")
		return nil, nil
	}
	return nil, fmt.Errorf("unknown command: %v", cmd)
}

func (s *captureControlSensor) Close(context.Context) error {
	return nil
}

func stringList(v interface{}) ([]string, bool) {
	raw, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func toAny(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
