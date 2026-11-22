package aws

// Services holds the AWS client of one session (a profile and a region) and
// the services that the views use. Add a new service here and in
// NewServices: the app builds every session through NewServices.
type Services struct {
	Client *Client
	S3     S3Service
	EC2    EC2Service
	SQS    SQSService
	Logs   LogsService
	Lambda LambdaService
	ECS    ECSService
}

// ForRegion returns services for another region of the same session, for
// reads only: every mutating call is refused. It builds no new client
// configuration (see Client.ForRegion).
func (s Services) ForRegion(region string) Services {
	c := s.Client.ForRegion(region)
	readOnly := func() bool { return true }
	return Services{
		Client: c,
		S3:     GuardS3(NewS3Service(c), readOnly),
		EC2:    GuardEC2(NewEC2Service(c), readOnly),
		SQS:    GuardSQS(NewSQSService(c), readOnly),
		Logs:   GuardLogs(NewLogsService(c), readOnly),
		Lambda: GuardLambda(NewLambdaService(c), readOnly),
		ECS:    GuardECS(NewECSService(c), readOnly),
	}
}

// NewServices builds the client and the services for a session. Each
// service refuses mutating calls while readOnly returns true (see
// readonly.go). When the client cannot be built, for example because the
// profile does not exist, NewServices returns the error and no services.
func NewServices(profile, region, endpoint string, readOnly func() bool) (Services, error) {
	c, err := NewClient(profile, region, endpoint)
	if err != nil {
		return Services{}, err
	}
	return Services{
		Client: c,
		S3:     GuardS3(NewS3Service(c), readOnly),
		EC2:    GuardEC2(NewEC2Service(c), readOnly),
		SQS:    GuardSQS(NewSQSService(c), readOnly),
		Logs:   GuardLogs(NewLogsService(c), readOnly),
		Lambda: GuardLambda(NewLambdaService(c), readOnly),
		ECS:    GuardECS(NewECSService(c), readOnly),
	}, nil
}
