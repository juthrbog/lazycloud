package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// App tests of the Lambda function list (docs/design/lambda.md, PR 2).

// :lambda NAME opens the list with a focus on that function. The aliases
// take it too. :lambda alone opens the list.
func TestLambdaCommandTakesAFunctionName(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.executeCommand("lambda orders-api")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "lambda_functions",
		Params: map[string]string{"focus": "orders-api"}}}, toAny(collectMsgs(cmd)))

	for _, alias := range []string{"fn", "functions"} {
		_, cmd = m.executeCommand(alias + "  thumbnailer ")
		assert.Equal(t, []any{msg.NavigateMsg{ViewID: "lambda_functions",
			Params: map[string]string{"focus": "thumbnailer"}}}, toAny(collectMsgs(cmd)), alias)
	}

	_, cmd = m.executeCommand("lambda")
	assert.Equal(t, []any{msg.NavigateMsg{ViewID: "lambda_functions"}}, toAny(collectMsgs(cmd)))
}

// The focus of :lambda NAME reaches the list after its load and opens the
// detail of the function.
func TestLambdaFocusOpensTheDetail(t *testing.T) {
	m := newTestModel(160, 40)
	mockAllServices(&m)
	svc := new(awstest.MockLambdaService)
	m.svc.Lambda = svc
	fn := aws.Function{Name: "orders-api", ARN: "arn:aws:lambda:us-east-1:1:function:orders-api"}
	svc.On("ListFunctionsPage", mock.Anything, mock.Anything).Return(&aws.FunctionPage{Functions: []aws.Function{fn}}, nil)
	svc.On("GetFunction", mock.Anything, "orders-api", "").Return(&aws.FunctionDetail{Function: fn, State: "Active"}, nil)
	svc.On("GetFunctionURL", mock.Anything, "orders-api").Return(nil, nil)

	m, cmd := update(t, m, msg.NavigateMsg{ViewID: "lambda_functions", Params: map[string]string{"focus": "orders-api"}})
	require.Equal(t, "lambda_functions", m.nav.Current().ID())
	m = pump(t, m, cmd)
	require.True(t, m.panels.IsOpen(), "the focus opens the detail")
	assert.Contains(t, rendered(m), "Active")
}
