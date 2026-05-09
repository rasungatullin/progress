package model

type LaunchSpec struct {
	Directory string
	Runner    string
	Model     string
	Prompt    string
}

type WorkplaceSpec struct {
	Name string
}

type Invocation struct {
	Task      string
	Profile   string
	Workplace WorkplaceSpec
	Launch    LaunchSpec
}

type Profile struct {
	Name  string
	Mode  string
	Model string
}

type Allocation struct {
	Resource string
	Reserved bool
}

type Workplace struct {
	Name  string
	Ready bool
}

type LaunchResult struct {
	Status  string
	Summary string
}
