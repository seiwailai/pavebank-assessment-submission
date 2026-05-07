@endpoint
Feature: Fees API health endpoint

  @EP_HEALTH_001
  Scenario: EP-HEALTH-001 health check returns service details
    When I send a "GET" request to "/v1/health/fees"
    Then the response status should be 200
    And the response JSON field "service" should equal "fees"
