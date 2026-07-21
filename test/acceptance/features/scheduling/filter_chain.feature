Feature: Scheduling filter chain
  Filters narrow the candidate endpoint list before scoring.

  Background:
    Given a scheduler with default configuration

  Scenario: All endpoints filtered out
    Given 3 endpoints that do not match the filter criteria
    When a completion request arrives for model "test-model"
    Then the scheduler returns an error

  Scenario: Some endpoints filtered out
    Given 3 endpoints where 1 matches the filter criteria
    When a completion request arrives for model "test-model"
    Then the request is routed to the matching endpoint
