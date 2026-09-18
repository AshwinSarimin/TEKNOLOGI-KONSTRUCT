import { Link, sidebarConfig } from '@backstage/core-components';
import { makeStyles } from '@material-ui/core';
import { LogoIcon } from './LogoIcon';

const useSidebarLogoStyles = makeStyles({
  root: {
    width: sidebarConfig.drawerWidthClosed,
    height: 3 * sidebarConfig.logoHeight,
    display: 'flex',
    flexFlow: 'row nowrap',
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: -14,
  },
  link: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
  },
});

export const SidebarLogo = () => {
  const classes = useSidebarLogoStyles();

  return (
    <div className={classes.root}>
      <Link to="/" underline="none" className={classes.link} aria-label="Home">
        <LogoIcon />
      </Link>
    </div>
  );
};
