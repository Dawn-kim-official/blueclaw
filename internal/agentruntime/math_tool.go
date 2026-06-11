package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"blueclaw/internal/agent"
)

const mathExpressionMaximumLength = 1024
const mathExpressionMaximumDepth = 32

func (toolCatalogBuilder *ToolCatalogBuilder) calculateMathTool(toolContext context.Context, input mathCalculateToolInput) (agent.ToolResult, error) {
	expression, errorValue := normalizeMathExpression(input.Expression)
	if errorValue != nil {
		return mathCalculateError(errorValue.Error(), "input_validation"), nil
	}
	result, errorValue := runBCExpression(toolContext, expression)
	if errorValue != nil {
		return mathCalculateError(errorValue.Error(), "bc_execution"), nil
	}
	return agent.ToolSuccess(marshalToolResult(map[string]string{
		"expression": strings.TrimSpace(input.Expression),
		"result":     result,
	})), nil
}

func normalizeMathExpression(expression string) (string, error) {
	trimmedExpression := strings.TrimSpace(expression)
	if trimmedExpression == "" {
		return "", errors.New("expression is required")
	}
	if len(trimmedExpression) > mathExpressionMaximumLength {
		return "", errors.New("expression is too long")
	}
	if !mathExpressionUsesAllowedCharacters(trimmedExpression) {
		return "", errors.New("expression contains unsupported characters")
	}
	if !mathExpressionParenthesesAreBalanced(trimmedExpression) {
		return "", errors.New("expression parentheses are not balanced")
	}
	return strings.ReplaceAll(trimmedExpression, "**", "^"), nil
}

func mathExpressionUsesAllowedCharacters(expression string) bool {
	for _, character := range expression {
		if character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case ' ', '\t', '\n', '\r', '.', '+', '-', '*', '/', '%', '^', '(', ')':
			continue
		default:
			return false
		}
	}
	return true
}

func mathExpressionParenthesesAreBalanced(expression string) bool {
	depth := 0
	for _, character := range expression {
		switch character {
		case '(':
			depth++
			if depth > mathExpressionMaximumDepth {
				return false
			}
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func runBCExpression(toolContext context.Context, expression string) (string, error) {
	commandContext, cancel := context.WithTimeout(toolContext, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, "bc", "-l")
	command.Stdin = strings.NewReader(expression + "\n")
	var standardOutput bytes.Buffer
	var standardError bytes.Buffer
	command.Stdout = &standardOutput
	command.Stderr = &standardError
	if errorValue := command.Run(); errorValue != nil {
		return "", bcExecutionError(commandContext, standardError.String(), errorValue)
	}
	result := strings.TrimSpace(standardOutput.String())
	if result == "" {
		return "", errors.New("bc returned an empty result")
	}
	return result, nil
}

func bcExecutionError(commandContext context.Context, standardError string, errorValue error) error {
	if commandContext.Err() != nil {
		return errors.New("bc timed out")
	}
	message := strings.TrimSpace(standardError)
	if message == "" {
		message = errorValue.Error()
	}
	return errors.New(message)
}

func mathCalculateError(message string, failureStage string) agent.ToolResult {
	return agent.ToolFailureResult(agent.FailureInvalidInput, agent.FailureCodes.InvalidInput, failureStage, message)
}

type mathCalculateToolInput struct {
	Expression string `json:"expression"`
}

func (toolCatalogBuilder *ToolCatalogBuilder) registerMathTool(toolRegistry *agent.ToolSet) {
	agent.RegisterToolFunction(toolRegistry, agent.ToolFunction[mathCalculateToolInput, agent.ToolResult]{
		Definition: agent.ToolDefinition{
			Name:        "math.calculate",
			Description: "Evaluate a safe arithmetic expression using bc. Supports numbers, parentheses, +, -, *, /, %, ^, and **.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`),
		},
		Handler: toolCatalogBuilder.calculateMathTool,
		Result:  agent.IdentityToolResult,
	})
}
