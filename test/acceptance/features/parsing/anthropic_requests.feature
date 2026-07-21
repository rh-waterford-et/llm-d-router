Feature: Anthropic request parsing
  The EPP parses Anthropic messages API requests.

  Scenario: Parse a messages request
    Given an Anthropic messages request with model "claude-3" and message "hello"
    When the Anthropic parser parses the request
    Then the parsed model is "claude-3"

  Scenario: Malformed JSON body
    Given a request body that is not valid JSON
    When the Anthropic parser attempts to parse the request
    Then parsing returns an error
