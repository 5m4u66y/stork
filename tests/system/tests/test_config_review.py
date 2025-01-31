from core.fixtures import kea_parametrize, ha_pair_parametrize
from core.wrappers import Server, Kea


@kea_parametrize("agent-kea-config-review")
def test_get_dhcp_config_review_reports(server_service: Server, kea_service: Kea):
    """Test that the Stork server performs Kea configuration review and returns
    the reports."""
    server_service.log_in_as_admin()
    server_service.authorize_all_machines()
    state, *_ = server_service.wait_for_next_machine_states()

    daemons = state['apps'][0]['details']['daemons']

    # DHCPv4 daemon.
    dhcp_v4_daemons = [d for d in daemons if d['name'] == 'dhcp4']
    assert len(dhcp_v4_daemons) == 1
    daemon_id = dhcp_v4_daemons[0]['id']

    # Get config reports for the daemon.
    data = server_service.wait_for_config_reports(daemon_id)

    # The response should include all generated reports, not only the ones with
    # issues.
    assert data['total'] > 5
    issue_reports = {report['checker']: report
                     for report in data['items']
                     if 'content' in report}
    assert len(issue_reports) == 5

    assert 'stat_cmds_presence' in issue_reports
    assert 'overlapping_subnet' in issue_reports
    assert 'canonical_prefix' in issue_reports
    assert 'address_pools_exhausted_by_reservations' in issue_reports

    # DHCPv6 daemon.
    dhcp_v6_daemons = [d for d in daemons if d['name'] == 'dhcp6']
    assert len(dhcp_v6_daemons) == 1
    daemon_id = dhcp_v6_daemons[0]['id']

    # Get config reports for the daemon.
    data = server_service.wait_for_config_reports(daemon_id)

    # The response should include all generated reports, not only the ones with
    # issues.
    assert data['total'] >= 2
    issue_reports = {report['checker']: report
                     for report in data['items']
                     if 'content' in report}
    assert len(issue_reports) == 2

    assert 'pd_pools_exhausted_by_reservations' in issue_reports


@ha_pair_parametrize('agent-kea-ha1-only-top-mt', 'agent-kea-ha2-only-top-mt')
def test_get_ha_pair_only_top_mt_config_review_reports(server_service: Server, ha_pair_service):
    """Test that the Stork server suggests to enable the HA multi-threading
    if the Kea is running in the multi-threading mode."""
    server_service.log_in_as_admin()
    server_service.authorize_all_machines()
    states = server_service.wait_for_next_machine_states()

    assert len(states) == 2

    for state in states:
        daemons = state['apps'][0]['details']['daemons']
        daemons = [d for d in daemons if d['name'] in ['dhcp4', 'dhcp6']]
        assert len(daemons) == 2

        for daemon in daemons:
            daemon_id = daemon['id']
            # Get config reports for the daemon.
            data = server_service.wait_for_config_reports(daemon_id)
            assert data['total'] > 1

            issue_reports = {report['checker']: report
                             for report in data['items']
                             if 'content' in report}

            assert 'ha_mt_presence' in issue_reports


@ha_pair_parametrize('agent-kea-ha1-mt', 'agent-kea-ha2-mt')
def test_get_ha_pair_mt_config_review_reports(server_service: Server, ha_pair_service):
    """Test that the Stork server suggests to use the dedicated listeners
    if the Kea HA is running in the multi-threading mode but the peers
    communicate over the Kea Control Agent."""
    server_service.log_in_as_admin()
    server_service.authorize_all_machines()
    states = server_service.wait_for_next_machine_states()

    assert len(states) == 2

    for state in states:
        daemons = state['apps'][0]['details']['daemons']
        daemons = [d for d in daemons if d['name'] in ['dhcp4', 'dhcp6']]
        assert len(daemons) == 2

        for daemon in daemons:
            daemon_id = daemon['id']
            # Get config reports for the daemon.
            data = server_service.wait_for_config_reports(daemon_id)
            assert data['total'] > 1

            issue_reports = {report['checker']: report
                             for report in data['items']
                             if 'content' in report}

            assert 'ha_mt_presence' not in issue_reports
            assert 'ha_dedicated_ports' in issue_reports
