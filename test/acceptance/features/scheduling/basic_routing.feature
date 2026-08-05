Feature: Basic request routing
  The EPP routes inference requests to model-serving pods based on
  endpoint scores and filter criteria.

  Background:
    Given a scheduler with default configuration

  Scenario: Route to the least-loaded endpoint
    Given 3 endpoints with queue depths 5, 2, and 8
    When a completion request arrives for model "test-model"
    Then the request is routed to the endpoint with queue depth 2

  Scenario: No available endpoints
    Given 0 endpoints
    When a completion request arrives for model "test-model"
    Then the scheduler returns an error

  Scenario: Single endpoint available
    Given 1 endpoint with queue depth 0
    When a completion request arrives for model "test-model"
    Then the request is routed to the only available endpoint

  Scenario: All endpoints at equal load
    Given 3 endpoints with queue depths 5, 5, and 5
    When a completion request arrives for model "test-model"
    Then the request is routed to one of the endpoints
