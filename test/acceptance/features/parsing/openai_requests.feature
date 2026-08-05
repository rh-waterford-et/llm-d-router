Feature: OpenAI request parsing
  The EPP parses OpenAI-format completion and chat requests.

  Scenario: Parse a completions request
    Given an OpenAI completions request with model "gpt-test" and prompt "hello"
    When the request is parsed
    Then the parsed model is "gpt-test"
    And the parsed prompt contains "hello"

  Scenario: Parse a chat completions request
    Given an OpenAI chat completions request with model "gpt-test" and message "hello"
    When the request is parsed
    Then the parsed model is "gpt-test"
    And the parsed body contains messages

  Scenario: Malformed JSON body
    Given a request body that is not valid JSON
    When the OpenAI parser attempts to parse the request
    Then parsing returns an error

  Scenario: Empty request body
    Given an empty request body
    When the OpenAI parser attempts to parse the request
    Then parsing returns an error

  Scenario: Parse request with multiple messages
    Given an OpenAI chat completions request with 3 messages
    When the request is parsed
    Then the parsed body contains messages
