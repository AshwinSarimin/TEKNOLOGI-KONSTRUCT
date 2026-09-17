import { Content, Header, Page } from '@backstage/core-components';
import { HomePageSearchBar } from '@backstage/plugin-search';
import {
  HomePageStarredEntities,
  HomePageToolkit,
} from '@backstage/plugin-home';
import { Grid, makeStyles } from '@material-ui/core';
import GitHubIcon from '@material-ui/icons/GitHub';
import CloudIcon from '@material-ui/icons/Cloud';

const useStyles = makeStyles(theme => ({
  searchBar: {
    display: 'flex',
    justifyContent: 'center',
    marginBottom: theme.spacing(3),
  },
}));

export const HomePage = () => {
  const classes = useStyles();
  return (
    <Page themeId="home">
      <Header
        title="TEKNOLOGI Platform"
        subtitle="Internal Developer Platform"
      />
      <Content>
        <div className={classes.searchBar}>
          <HomePageSearchBar />
        </div>
        <Grid container spacing={3}>
          <Grid item xs={12} md={6}>
            <HomePageStarredEntities />
          </Grid>
          <Grid item xs={12} md={6}>
            <HomePageToolkit
              tools={[
                {
                  url: 'https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT',
                  label: 'Platform Repo',
                  icon: <GitHubIcon />,
                },
                {
                  url: 'https://portal.azure.com',
                  label: 'Azure Portal',
                  icon: <CloudIcon />,
                },
              ]}
            />
          </Grid>
        </Grid>
      </Content>
    </Page>
  );
};
