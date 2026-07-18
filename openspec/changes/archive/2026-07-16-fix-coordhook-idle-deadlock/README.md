# fix-coordhook-idle-deadlock

Coord-hook stops re-blocking a coordinator that already requested recycle, and force-recycles at 1.5x budget if idle never comes
