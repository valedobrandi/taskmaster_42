package internal

type Restart struct {
	Start   []string
	Stop    []string
	Restart []string
	Update  []string
}

func Restarting(instances map[string]*Instance, configs map[string]*Config) Restart {
	sequence := Restart{}

	for name, inst := range instances {
		newConfig, exists := configs[name]
		if !exists {
			sequence.Stop = append(sequence.Stop, name)
			continue
		}
		if isConfigChanged(inst.spec.Load(), newConfig) {
			sequence.Restart = append(sequence.Restart, name)
			continue
		}

		sequence.Update = append(sequence.Update, name)
	}

	for name := range configs {
		if _, exists := instances[name]; !exists {
			sequence.Start = append(sequence.Start, name)
		}
	}

	return sequence
}
