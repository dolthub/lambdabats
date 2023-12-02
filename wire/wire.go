package wire

type RunTestRequest struct {
	TestLocation string `json:"test_location"`
	FileName     string `json:"file_name"`
	TestName     string `json:"test_name"`
}

type RunTestResult struct {
	Output string `json:"output"`
	Err    string `json:"err"`
}
