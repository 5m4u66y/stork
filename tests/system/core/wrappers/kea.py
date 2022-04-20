import csv
import io
from core.compose import DockerCompose
from core.wrappers.base import ComposeServiceWrapper
from core.wrappers.server import Server
from core.wrappers.agent import Agent


class Kea(Agent):
    def __init__(self, compose: DockerCompose, service_name: str,
                 server_service: Server):
        super().__init__(compose, service_name, server_service)

    def read_lease_file(self, family: int):
        path = '/var/lib/kea/kea-leases%d.csv' % family
        cmd = ["cat", path]
        _, stdout, _ = self._compose.exec_in_container(
            self._service_name, cmd)

        return csv.DictReader(io.StringIO(stdout))
