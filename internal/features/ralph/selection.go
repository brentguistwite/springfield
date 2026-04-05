package ralph

// FindStory returns the story with the requested ID.
func (p Plan) FindStory(id string) (Story, bool) {
	for _, story := range p.Spec.Stories {
		if story.ID == id {
			return story, true
		}
	}

	return Story{}, false
}

// NextEligible returns the next unpassed story whose dependencies are satisfied.
// Stories are considered in their declared order.
func (p Plan) NextEligible() (Story, bool) {
	passed := make(map[string]bool, len(p.Spec.Stories))
	for _, story := range p.Spec.Stories {
		if story.Passed {
			passed[story.ID] = true
		}
	}

	for _, story := range p.Spec.Stories {
		if story.Passed {
			continue
		}

		if dependenciesPassed(story.DependsOn, passed) {
			return story, true
		}
	}

	return Story{}, false
}

func dependenciesPassed(dependsOn []string, passed map[string]bool) bool {
	for _, dependency := range dependsOn {
		if !passed[dependency] {
			return false
		}
	}

	return true
}
