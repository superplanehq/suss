package plan

import "fmt"

// Validate reports contract invariants that JSON Schema cannot express.
// The primary check is that a command with a null run must be a declared
// task referenced by an ambiguity in the same project via commandId.
func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion %q, want %q", d.SchemaVersion, SchemaVersion)
	}
	if d.Projects == nil {
		return fmt.Errorf("projects must be an array")
	}

	seenIDs := make(map[CommandID]string)
	for i, project := range d.Projects {
		if err := project.validate(i, seenIDs); err != nil {
			return err
		}
	}
	return nil
}

func (p ProjectPlan) validate(index int, seenIDs map[CommandID]string) error {
	if p.Path == "" {
		return fmt.Errorf("projects[%d].path must not be empty", index)
	}
	if err := requireArray(p.Languages, index, "languages"); err != nil {
		return err
	}
	if err := requireArray(p.Frameworks, index, "frameworks"); err != nil {
		return err
	}
	if err := requireArray(p.PackageManagers, index, "packageManagers"); err != nil {
		return err
	}
	if err := requireArray(p.Facts, index, "facts"); err != nil {
		return err
	}
	if err := requireArray(p.Requirements, index, "requirements"); err != nil {
		return err
	}
	if err := requireArray(p.Preparation, index, "preparation"); err != nil {
		return err
	}
	if err := requireArray(p.Commands, index, "commands"); err != nil {
		return err
	}
	if err := requireArray(p.Ambiguities, index, "ambiguities"); err != nil {
		return err
	}
	if err := requireArray(p.Conflicts, index, "conflicts"); err != nil {
		return err
	}

	commands := make(map[CommandID]Command, len(p.Preparation)+len(p.Commands))
	for _, command := range p.Preparation {
		if err := indexCommand(index, "preparation", command, commands, seenIDs, p.Path); err != nil {
			return err
		}
	}
	for _, command := range p.Commands {
		if err := indexCommand(index, "commands", command, commands, seenIDs, p.Path); err != nil {
			return err
		}
	}

	referenced := make(map[CommandID]struct{})
	for _, ambiguity := range p.Ambiguities {
		if ambiguity.CommandID == nil {
			continue
		}
		if _, ok := commands[*ambiguity.CommandID]; !ok {
			return fmt.Errorf("projects[%d] ambiguity %q references unknown command %q", index, ambiguity.Subject, *ambiguity.CommandID)
		}
		referenced[*ambiguity.CommandID] = struct{}{}
	}
	for _, conflict := range p.Conflicts {
		if conflict.CommandID == nil {
			continue
		}
		if _, ok := commands[*conflict.CommandID]; !ok {
			return fmt.Errorf("projects[%d] conflict %q references unknown command %q", index, conflict.Subject, *conflict.CommandID)
		}
	}

	for id, command := range commands {
		if command.Run != nil {
			continue
		}
		if command.Origin != CommandDeclared {
			return fmt.Errorf("projects[%d] command %q has a null run but origin %q; null run is only legal on declared commands", index, id, command.Origin)
		}
		if _, ok := referenced[id]; !ok {
			return fmt.Errorf("projects[%d] command %q has a null run but is not referenced by an ambiguity via commandId", index, id)
		}
	}

	return nil
}

func indexCommand(projectIndex int, collection string, command Command, commands map[CommandID]Command, seenIDs map[CommandID]string, projectPath string) error {
	if command.ID == "" {
		return fmt.Errorf("projects[%d].%s contains a command with an empty id", projectIndex, collection)
	}
	if _, exists := commands[command.ID]; exists {
		return fmt.Errorf("projects[%d] duplicates command id %q", projectIndex, command.ID)
	}
	if previous, exists := seenIDs[command.ID]; exists {
		return fmt.Errorf("command id %q is used by both %s and %s", command.ID, previous, projectPath)
	}
	commands[command.ID] = command
	seenIDs[command.ID] = projectPath
	return nil
}

func requireArray[T any](values []T, projectIndex int, name string) error {
	if values == nil {
		return fmt.Errorf("projects[%d].%s must be an array", projectIndex, name)
	}
	return nil
}
