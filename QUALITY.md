# Regression testing
Regressions will be run periodically by QE, in order to ensure that everything is functional.  This should be done before every release, including new builds of previous releases.  Nothing should be delivered to customers that QE hasn't run a regression on and signed off on.

# Feature Testing
## Red Hat requirements
Red Had developers will do initial validation as part of delivery.  Red Hat QE will do verification to ensure everything works correctly, before delivering to customer.
## External contributor features
They are responsible for testing.  Test strategy should be reviewed before it is merged.

# Bug fixing
## Upstream Bugs
In order to maintain a high level of quality upstream, Red Hat developers will make a point of fixing any bugs raised in upstream PTP project.  While priority may be given based on business requirements, as maintainers of the upstream PTP project, Red Hat developers will eventually fix all legitimate upstream bugs, regardless of who raises it.  However, other partners and contributors to the upstream project will be strongly encouraged to fix bugs too, especially those that they raise or are primarily impacted by.
## Customer Bugs
Any bugs raised by customers should be given priority in investigation.  Red Hat developers will follow these steps when addressing bugs:
1. Determine whether the behaviour the customer describes is intended
    1. If behaving as intended, inform customer
    2. If customer pushes issue, they can raise with Red Hat business team
2. If so, determine whether this is a user error causing this to occur,
If user error, inform customer of what they’re doing incorrect
3. If a legitimate issue, determine whether this has been fixed already in later release
If so, backport
4. If not fixed yet, determine if reproducible upstream
    1. If reproducible upstream, fix upstream and backport
    2. If not reproducible upstream, was it unintentionally fixed?
        1. If so, backport
        2. If not, find where it was introduced through cherry pick and handle appropriately.




