import { Component, Input } from '@angular/core'
import { LocalSubnet, Subnet } from '../backend'
import { hasAddressPools, hasPrefixPools } from '../subnets'
import { hasDifferentLocalSubnetPools } from '../subnets'

@Component({
    selector: 'app-subnet-tab',
    templateUrl: './subnet-tab.component.html',
    styleUrls: ['./subnet-tab.component.sass'],
})
export class SubnetTabComponent {
    /**
     * Subnet data.
     */
    @Input() subnet: Subnet

    /**
     * Checks if the subnet has IPv6 type.
     *
     * @return true if the subnet has IPv6 type, false otherwise.
     */
    get isIPv6(): boolean {
        return this.subnet.subnet.includes(':')
    }

    /**
     * Returns attributes used in constructing a link to a shared network.
     *
     * @returns a map of attributes including shared network name and a universe.
     */
    getSharedNetworkAttrs() {
        return {
            text: this.subnet.sharedNetwork,
            dhcpVersion: this.isIPv6 ? 6 : 4,
        }
    }

    /**
     * Checks if the subnet has any address pools.
     *
     * @returns true if the subnet has any address pools, false otherwise.
     */
    subnetHasAddressPools(): boolean {
        return hasAddressPools(this.subnet)
    }

    /**
     * Checks if the subnet has any prefix pools.
     *
     * @returns true if the subnet has any prefix pools, false otherwise.
     */
    subnetHasPrefixPools(): boolean {
        return hasPrefixPools(this.subnet)
    }

    /**
     * Check if all daemons using the subnet have the same configured pools.
     *
     * Usually all servers have the same set of pools configured for a subnet.
     * However, it is also a valid use case for the servers to have different
     * pools. In that case, the component must display the pools for individual
     * servers separately. This function checks if this is the case.
     *
     * @returns true if all servers have the same set of pools for a subnet,
     * false otherwise.
     */
    allDaemonsHaveEqualPools(): boolean {
        return !hasDifferentLocalSubnetPools(this.subnet)
    }
}
