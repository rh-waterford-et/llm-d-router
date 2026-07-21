Feature: Flow control admission
  The flow control system enforces capacity limits on incoming requests.

  Scenario: Request admitted when under capacity
    Given a saturation detector with max concurrency 10
    And 5 requests are currently in flight
    When a new request arrives
    Then the saturation level is below 1.0

  Scenario: Saturation at capacity
    Given a saturation detector with max concurrency 10
    And 10 requests are currently in flight
    When the saturation level is checked
    Then the saturation level is 1.0

  Scenario: Saturation returns to zero
    Given a saturation detector with max concurrency 10
    And 5 requests are tracked and then released
    When the saturation level is checked
    Then the saturation level is 0.0

  Scenario: Concurrent saturation reads are safe
    Given a saturation detector with max concurrency 100
    When 50 requests are tracked and released concurrently
    Then all saturation readings are in the range 0.0 to 1.0
