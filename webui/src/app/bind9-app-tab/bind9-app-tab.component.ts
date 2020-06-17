import { Component, OnInit, Input, Output, EventEmitter } from '@angular/core'

import moment from 'moment-timezone'

import { MessageService, MenuItem } from 'primeng/api'

import {
    durationToString,
    daemonStatusErred,
    daemonStatusIconName,
    daemonStatusIconColor,
    daemonStatusIconTooltip,
} from '../utils'

@Component({
    selector: 'app-bind9-app-tab',
    templateUrl: './bind9-app-tab.component.html',
    styleUrls: ['./bind9-app-tab.component.sass'],
})
export class Bind9AppTabComponent implements OnInit {
    private _appTab: any
    @Output() refreshApp = new EventEmitter<number>()

    daemons: any[] = []

    constructor() {}

    ngOnInit() {}

    @Input()
    set appTab(appTab) {
        this._appTab = appTab

        const daemonMap = []
        daemonMap[appTab.app.details.daemon.name] = appTab.app.details.daemon
        const DMAP = [['named', 'named']]
        const daemons = []
        for (const dm of DMAP) {
            if (daemonMap[dm[0]] !== undefined) {
                daemonMap[dm[0]].niceName = dm[1]
                daemons.push(daemonMap[dm[0]])
            }
        }
        this.daemons = daemons
    }

    get appTab() {
        return this._appTab
    }

    refreshAppState() {
        this.refreshApp.emit(this._appTab.app.id)
    }

    showDuration(duration) {
        return durationToString(duration)
    }

    /**
     * Get cache effectiveness based on stats.
     * A percentage is returned as floored int.
     */
    getQueryUtilization(daemon) {
        let utilization = 0.0
        if (!daemon.queryHitRatio) {
            return utilization
        }
        utilization = 100 * daemon.queryHitRatio
        return Math.floor(utilization)
    }

    /**
     * Returns boolean value indicating if there is an issue with communication
     * with the given daemon
     *
     * @param daemon data structure holding the information about the daemon.
     *
     * @return true if there is a communication problem with the daemon,
     *         false otherwise.
     */
    daemonStatusErred(daemon) {
        return daemon.active && daemonStatusErred(daemon)
    }

    /**
     * Returns the name of the icon to be used when presenting daemon status
     *
     * The icon selected depends on whether the daemon is active or not
     * active and whether there is a communication with the daemon or
     * not.
     *
     * @param daemon data structure holding the information about the daemon.
     *
     * @returns ban icon if the daemon is not active, times icon if the daemon
     *          should be active but the communication with it is borken and
     *          check icon if the communication with the active daemon is ok.
     */
    daemonStatusIconName(daemon) {
        return daemonStatusIconName(daemon)
    }

    /**
     * Returns the color of the icon used when presenting daemon status
     *
     * @param daemon data structure holding the information about the daemon.
     *
     * @returns grey color if the daemon is not active, red if the daemon is
     *          active but there are communication issues, green if the
     *          communication with the active daemon is ok.
     */
    daemonStatusIconColor(daemon) {
        return daemonStatusIconColor(daemon)
    }

    /**
     * Returns error text to be displayed when there is a communication issue
     * with a given daemon
     *
     * @param daemon data structure holding the information about the daemon.
     *
     * @returns Error text. It includes hints about the communication
     *          problems when such problems occur, e.g. it includes the
     *          hint whether the communication is with the agent or daemon.
     */
    daemonStatusErrorText(daemon) {
        return daemonStatusIconTooltip(daemon)
    }
}
